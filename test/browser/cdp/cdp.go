// Package cdp is the default browser driver: Chrome or Chromium, driven over
// the Chrome DevTools Protocol through the debugging pipe the browser opens on
// its file descriptors 3 and 4 (--remote-debugging-pipe).
//
// It is the standard library and nothing else. There is no port to allocate
// or leak, no WebSocket to frame, and nothing for another process on the
// machine to connect to: the pipe belongs to the browser this driver started,
// and closes with it. Each Launch starts its own browser with its own profile
// directory, so two drivers share no cookie, storage or cache.
//
// Register it in the application's test case:
//
//	var Drivers = browser.Registry{"cdp": cdp.Launch}
//
// The browser is CHROME_PATH when it is set, and otherwise the first Chrome or
// Chromium found in the standard locations; with none, Launch returns an error
// wrapping browser.ErrNoBrowser, which skips the test on a developer's machine
// and fails it in CI. Headed runs and slow motion come from browser.Options.
//
// The pipe is a pair of inherited descriptors, which Windows does not offer to
// a child process the same way; there Launch returns an error.
package cdp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/test/browser"
)

// candidates are where a browser is looked for after CHROME_PATH: the macOS
// application bundles, then the names a Linux distribution installs on PATH.
var candidates = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"chrome",
}

// Find returns the browser Launch would start: CHROME_PATH, or the first of
// the standard locations that exists. With none it returns an error wrapping
// browser.ErrNoBrowser that names every place it looked.
func Find() (string, error) {
	return find(os.Getenv("CHROME_PATH"), candidates)
}

func find(explicit string, places []string) (string, error) {
	if explicit != "" {
		path, err := exec.LookPath(explicit)
		if err != nil {
			return "", fmt.Errorf("cdp: CHROME_PATH=%s is not an executable: %w", explicit, err)
		}
		return path, nil
	}
	for _, place := range places {
		if path, err := exec.LookPath(place); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: looked in CHROME_PATH, then %s; install Chrome or set CHROME_PATH",
		browser.ErrNoBrowser, strings.Join(places, ", "))
}

// Driver is one Chrome process with one page, driven over its pipe.
type Driver struct {
	conn    *conn
	cmd     *exec.Cmd
	exited  chan struct{}
	dir     string
	stderr  *tail
	session string
	frame   string
	version string
	slowmo  time.Duration

	mu       sync.Mutex
	listener func(browser.Event)
	loads    chan string
	methods  map[string]string

	closeOnce sync.Once
	closeErr  error
}

var _ browser.Driver = (*Driver)(nil)

// Launch starts a browser and attaches to its page. ctx bounds the start only;
// the browser runs until Close. It matches browser.Factory.
func Launch(ctx context.Context, opts browser.Options) (browser.Driver, error) {
	switch strings.ToLower(opts.Browser) {
	case "", "chromium", "chrome":
	default:
		return nil, fmt.Errorf("cdp: drives Chrome and Chromium only, and ARANDU_E2E_BROWSER asks for %q; register a driver for it", opts.Browser)
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("cdp: the debugging pipe needs inherited file descriptors 3 and 4, which Windows does not provide")
	}
	path, err := Find()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "arandu-cdp-")
	if err != nil {
		return nil, fmt.Errorf("cdp: profile directory: %w", err)
	}

	// The browser reads commands from fd 3 and writes replies to fd 4.
	browserIn, toBrowser, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("cdp: pipe: %w", err)
	}
	fromBrowser, browserOut, err := os.Pipe()
	if err != nil {
		_ = browserIn.Close()
		_ = toBrowser.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("cdp: pipe: %w", err)
	}

	d := &Driver{
		dir:     dir,
		stderr:  &tail{},
		slowmo:  opts.SlowMo,
		exited:  make(chan struct{}),
		loads:   make(chan string, 32),
		methods: map[string]string{},
	}
	d.cmd = exec.Command(path, arguments(dir, opts.Headed)...)
	d.cmd.ExtraFiles = []*os.File{browserIn, browserOut}
	d.cmd.Stderr = d.stderr
	if err := d.cmd.Start(); err != nil {
		for _, f := range []*os.File{browserIn, browserOut, toBrowser, fromBrowser} {
			_ = f.Close()
		}
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("cdp: start %s: %w", path, err)
	}
	_ = browserIn.Close()
	_ = browserOut.Close()
	go func() {
		_ = d.cmd.Wait()
		close(d.exited)
	}()
	d.conn = newConn(fromBrowser, toBrowser, d.explain)
	go func() {
		<-d.conn.done
		_ = fromBrowser.Close()
	}()

	if err := d.attach(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("cdp: launch %s: %w", path, err)
	}
	return d, nil
}

// arguments are the browser's flags: a profile of its own, the pipe, and
// every background service that could make a run differ from the last turned
// off.
func arguments(dir string, headed bool) []string {
	args := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
		"--disable-breakpad",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--disable-features=Translate,OptimizationHints,MediaRouter,DialMediaRouteProvider",
		"--metrics-recording-only",
		"--mute-audio",
		"--password-store=basic",
		"--use-mock-keychain",
		"--force-color-profile=srgb",
		"--window-size=1440,900",
	}
	if !headed {
		args = append(args, "--headless=new")
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		// Chrome refuses to start its sandbox as root, which is how most CI
		// containers run.
		args = append(args, "--no-sandbox")
	}
	return append(args, "about:blank")
}

// explain adds what the browser printed before the pipe closed, which is
// where a browser that could not start says why.
func (d *Driver) explain(err error) error {
	if out := strings.TrimSpace(d.stderr.String()); out != "" {
		return fmt.Errorf("%w; the browser wrote: %s", err, out)
	}
	return err
}

// attach finds the page target, attaches a flat session to it and enables
// the domains the driver reports from.
func (d *Driver) attach(ctx context.Context) error {
	var version struct {
		Product string `json:"product"`
	}
	if err := d.conn.call(ctx, "", "Browser.getVersion", nil, &version); err != nil {
		return err
	}
	d.version = version.Product

	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := d.conn.call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return err
	}
	target := ""
	for _, info := range targets.TargetInfos {
		if info.Type == "page" {
			target = info.TargetID
			break
		}
	}
	if target == "" {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := d.conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
			return err
		}
		target = created.TargetID
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := d.conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, &attached); err != nil {
		return err
	}
	d.session = attached.SessionID
	d.conn.listen(d.session, d.handle)

	for _, method := range []string{"Page.enable", "Runtime.enable", "Network.enable", "Log.enable"} {
		if err := d.call(ctx, method, nil, nil); err != nil {
			return err
		}
	}
	if err := d.call(ctx, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}, nil); err != nil {
		return err
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := d.call(ctx, "Page.getFrameTree", nil, &tree); err != nil {
		return err
	}
	d.mu.Lock()
	d.frame = tree.FrameTree.Frame.ID
	d.mu.Unlock()
	return nil
}

func (d *Driver) call(ctx context.Context, method string, params, result any) error {
	return d.conn.call(ctx, d.session, method, params, result)
}

// Info names the driver and the browser's version.
func (d *Driver) Info() browser.Info {
	return browser.Info{Driver: "cdp", Browser: "chromium", Version: d.version}
}

// Listen hands every event from now on to fn.
func (d *Driver) Listen(fn func(browser.Event)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listener = fn
}

func (d *Driver) emit(e browser.Event) {
	d.mu.Lock()
	fn := d.listener
	d.mu.Unlock()
	if fn != nil {
		e.Time = time.Now()
		fn(e)
	}
}

// handle translates the session's events. It runs on the reading goroutine.
func (d *Driver) handle(method string, raw json.RawMessage) {
	switch method {
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Type                string          `json:"type"`
				Value               json.RawMessage `json:"value"`
				UnserializableValue string          `json:"unserializableValue"`
				Description         string          `json:"description"`
			} `json:"args"`
			StackTrace struct {
				CallFrames []struct {
					URL string `json:"url"`
				} `json:"callFrames"`
			} `json:"stackTrace"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		parts := make([]string, 0, len(p.Args))
		for _, a := range p.Args {
			switch {
			case a.Type == "string" && len(a.Value) > 0:
				var s string
				_ = json.Unmarshal(a.Value, &s)
				parts = append(parts, s)
			case len(a.Value) > 0:
				parts = append(parts, string(a.Value))
			case a.UnserializableValue != "":
				parts = append(parts, a.UnserializableValue)
			default:
				parts = append(parts, a.Description)
			}
		}
		e := browser.Event{Kind: browser.EventConsole, Level: consoleLevel(p.Type), Source: "console-api", Text: strings.Join(parts, " ")}
		if len(p.StackTrace.CallFrames) > 0 {
			e.URL = p.StackTrace.CallFrames[0].URL
		}
		d.emit(e)
	case "Runtime.exceptionThrown":
		var p struct {
			Details struct {
				Text      string `json:"text"`
				URL       string `json:"url"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		text := p.Details.Exception.Description
		if text == "" {
			text = p.Details.Text
		}
		d.emit(browser.Event{Kind: browser.EventException, Text: text, URL: p.Details.URL})
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Source string `json:"source"`
				Level  string `json:"level"`
				Text   string `json:"text"`
				URL    string `json:"url"`
			} `json:"entry"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		level := p.Entry.Level
		if level == "verbose" {
			level = "debug"
		}
		d.emit(browser.Event{Kind: browser.EventConsole, Level: level, Source: p.Entry.Source, Text: p.Entry.Text, URL: p.Entry.URL})
	case "Network.requestWillBeSent":
		var p struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Request   struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
			RedirectResponse *response `json:"redirectResponse"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		if p.RedirectResponse != nil {
			d.emit(browser.Event{Kind: browser.EventResponse, RequestID: p.RequestID, Method: d.methods[p.RequestID], URL: p.RedirectResponse.URL,
				ResourceType: p.Type, Status: p.RedirectResponse.Status, Header: p.RedirectResponse.header()})
		}
		d.methods[p.RequestID] = p.Request.Method
		d.emit(browser.Event{Kind: browser.EventRequest, RequestID: p.RequestID, Method: p.Request.Method, URL: p.Request.URL, ResourceType: p.Type})
	case "Network.responseReceived":
		var p struct {
			RequestID string   `json:"requestId"`
			Type      string   `json:"type"`
			Response  response `json:"response"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		d.emit(browser.Event{Kind: browser.EventResponse, RequestID: p.RequestID, Method: d.methods[p.RequestID], URL: p.Response.URL,
			ResourceType: p.Type, Status: p.Response.Status, Header: p.Response.header()})
	case "Network.loadingFinished":
		var p struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		delete(d.methods, p.RequestID)
		d.emit(browser.Event{Kind: browser.EventRequestFinished, RequestID: p.RequestID})
	case "Network.loadingFailed":
		var p struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
			Type      string `json:"type"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		delete(d.methods, p.RequestID)
		d.emit(browser.Event{Kind: browser.EventRequestFailed, RequestID: p.RequestID, Text: p.ErrorText, ResourceType: p.Type})
	case "Page.frameStartedLoading", "Page.frameStoppedLoading":
		var p struct {
			FrameID string `json:"frameId"`
		}
		if json.Unmarshal(raw, &p) != nil || !d.isMain(p.FrameID) {
			return
		}
		kind := browser.EventNavigationStarted
		if method == "Page.frameStoppedLoading" {
			kind = browser.EventNavigationFinished
		}
		d.emit(browser.Event{Kind: kind})
	case "Page.lifecycleEvent":
		var p struct {
			FrameID  string `json:"frameId"`
			LoaderID string `json:"loaderId"`
			Name     string `json:"name"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Name != "load" || !d.isMain(p.FrameID) {
			return
		}
		select {
		case d.loads <- p.LoaderID:
		default:
		}
	}
}

func (d *Driver) isMain(frame string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return frame != "" && frame == d.frame
}

type response struct {
	URL     string            `json:"url"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

func (r *response) header() http.Header {
	h := http.Header{}
	for name, value := range r.Headers {
		h.Set(name, value)
	}
	return h
}

func consoleLevel(kind string) string {
	switch kind {
	case "error", "assert":
		return "error"
	case "warning":
		return "warning"
	case "info":
		return "info"
	case "debug":
		return "debug"
	}
	return "log"
}

// slow waits the slow-motion delay, when there is one.
func (d *Driver) slow(ctx context.Context) error {
	if d.slowmo <= 0 {
		return nil
	}
	timer := time.NewTimer(d.slowmo)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Navigate loads url and waits for the main frame's load event of that
// navigation.
func (d *Driver) Navigate(ctx context.Context, url string) error {
	if err := d.slow(ctx); err != nil {
		return err
	}
	for drained := false; !drained; {
		select {
		case <-d.loads:
		default:
			drained = true
		}
	}
	var nav struct {
		FrameID   string `json:"frameId"`
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if err := d.call(ctx, "Page.navigate", map[string]any{"url": url}, &nav); err != nil {
		return err
	}
	if nav.ErrorText != "" {
		return fmt.Errorf("navigate to %s: %s", url, nav.ErrorText)
	}
	if nav.LoaderID == "" {
		return nil // a same-document navigation has no load to wait for
	}
	for {
		select {
		case loader := <-d.loads:
			if loader == nav.LoaderID {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("navigate to %s: the page did not finish loading: %w", url, ctx.Err())
		case <-d.conn.done:
			return fmt.Errorf("navigate to %s: %w", url, d.conn.closed())
		}
	}
}

// documentGone are the errors Chrome answers an evaluation with when the
// document it ran in was replaced under it.
var documentGone = []string{
	"Execution context was destroyed",
	"Cannot find context with specified id",
	"Cannot find default execution context",
	"Inspected target navigated or closed",
	"Promise was collected",
}

// Evaluate runs expression in the page and decodes its value into result.
func (d *Driver) Evaluate(ctx context.Context, expression string, result any) error {
	var r struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := d.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &r)
	if err != nil {
		for _, gone := range documentGone {
			if strings.Contains(err.Error(), gone) {
				return fmt.Errorf("%w: %w", browser.ErrDocumentChanged, err)
			}
		}
		return err
	}
	if r.ExceptionDetails != nil {
		text := r.ExceptionDetails.Exception.Description
		if text == "" {
			text = r.ExceptionDetails.Text
		}
		return fmt.Errorf("javascript: %s", text)
	}
	if result == nil {
		return nil
	}
	if len(r.Result.Value) == 0 {
		return fmt.Errorf("javascript: the expression evaluated to %s, which has no JSON value", r.Result.Type)
	}
	if err := json.Unmarshal(r.Result.Value, result); err != nil {
		return fmt.Errorf("javascript: decode the value: %w", err)
	}
	return nil
}

// AddInitScript runs source in every document from now on.
func (d *Driver) AddInitScript(ctx context.Context, source string) error {
	return d.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": source}, nil)
}

// Click moves the mouse to x, y, presses the left button and releases it.
func (d *Driver) Click(ctx context.Context, x, y float64) error {
	if err := d.slow(ctx); err != nil {
		return err
	}
	for _, e := range []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 0, "clickCount": 1},
	} {
		if err := d.call(ctx, "Input.dispatchMouseEvent", e, nil); err != nil {
			return err
		}
	}
	return nil
}

// InsertText types text into the focused element.
func (d *Driver) InsertText(ctx context.Context, text string) error {
	if err := d.slow(ctx); err != nil {
		return err
	}
	return d.call(ctx, "Input.insertText", map[string]any{"text": text}, nil)
}

// key is what Chrome needs to synthesise one key.
type key struct {
	key, code, text string
	vk              int
}

var keys = map[string]key{
	"Enter":      {"Enter", "Enter", "\r", 13},
	"Tab":        {"Tab", "Tab", "", 9},
	"Escape":     {"Escape", "Escape", "", 27},
	"Backspace":  {"Backspace", "Backspace", "", 8},
	"Delete":     {"Delete", "Delete", "", 46},
	"Space":      {" ", "Space", " ", 32},
	"ArrowLeft":  {"ArrowLeft", "ArrowLeft", "", 37},
	"ArrowUp":    {"ArrowUp", "ArrowUp", "", 38},
	"ArrowRight": {"ArrowRight", "ArrowRight", "", 39},
	"ArrowDown":  {"ArrowDown", "ArrowDown", "", 40},
	"Home":       {"Home", "Home", "", 36},
	"End":        {"End", "End", "", 35},
	"PageUp":     {"PageUp", "PageUp", "", 33},
	"PageDown":   {"PageDown", "PageDown", "", 34},
}

var modifiers = map[string]int{"Alt": 1, "Control": 2, "Ctrl": 2, "Meta": 4, "Shift": 8}

// Press presses and releases a key, with any modifiers named before it.
func (d *Driver) Press(ctx context.Context, name string) error {
	if err := d.slow(ctx); err != nil {
		return err
	}
	parts := strings.Split(name, "+")
	mask := 0
	for _, m := range parts[:len(parts)-1] {
		bit, ok := modifiers[m]
		if !ok {
			return fmt.Errorf("press %q: unknown modifier %q", name, m)
		}
		mask |= bit
	}
	last := parts[len(parts)-1]
	k, ok := keys[last]
	if !ok {
		if len([]rune(last)) != 1 {
			return fmt.Errorf("press %q: unknown key %q", name, last)
		}
		k = key{key: last, text: last}
		if r := []rune(strings.ToUpper(last))[0]; r >= 'A' && r <= 'Z' {
			k.code, k.vk = "Key"+string(r), int(r)
		} else if r >= '0' && r <= '9' {
			k.code, k.vk = "Digit"+string(r), int(r)
		}
	}
	if mask&(2|4|1) != 0 {
		k.text = "" // a chord types nothing
	}
	down := map[string]any{"type": "rawKeyDown", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": mask}
	if k.text != "" {
		down["type"], down["text"], down["unmodifiedText"] = "keyDown", k.text, k.text
	}
	if err := d.call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return d.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": mask}, nil)
}

// SetViewport resizes the page.
func (d *Driver) SetViewport(ctx context.Context, v browser.Viewport) error {
	scale := v.Scale
	if scale == 0 {
		scale = 1
	}
	return d.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": v.Width, "height": v.Height, "deviceScaleFactor": scale, "mobile": v.Mobile,
	}, nil)
}

// EmulateMedia sets the media type and the preferences media queries read.
func (d *Driver) EmulateMedia(ctx context.Context, m browser.Media) error {
	features := []map[string]string{}
	if m.ColorScheme != "" {
		features = append(features, map[string]string{"name": "prefers-color-scheme", "value": m.ColorScheme})
	}
	if m.ReducedMotion != "" {
		features = append(features, map[string]string{"name": "prefers-reduced-motion", "value": m.ReducedMotion})
	}
	return d.call(ctx, "Emulation.setEmulatedMedia", map[string]any{"media": m.Type, "features": features}, nil)
}

// Screenshot captures the viewport as PNG.
func (d *Driver) Screenshot(ctx context.Context) ([]byte, error) {
	var shot struct {
		Data string `json:"data"`
	}
	if err := d.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, &shot); err != nil {
		return nil, err
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return nil, fmt.Errorf("cdp: screenshot: %w", err)
	}
	return png, nil
}

// SetCookies stores cookies as if url had set them.
func (d *Driver) SetCookies(ctx context.Context, url string, cookies []*http.Cookie) error {
	list := make([]map[string]any, 0, len(cookies))
	for _, c := range cookies {
		item := map[string]any{"name": c.Name, "value": c.Value, "url": url, "secure": c.Secure, "httpOnly": c.HttpOnly}
		if c.Domain != "" {
			item["domain"] = c.Domain
		}
		if c.Path != "" {
			item["path"] = c.Path
		}
		if !c.Expires.IsZero() {
			item["expires"] = c.Expires.Unix()
		}
		switch c.SameSite {
		case http.SameSiteLaxMode:
			item["sameSite"] = "Lax"
		case http.SameSiteStrictMode:
			item["sameSite"] = "Strict"
		case http.SameSiteNoneMode:
			item["sameSite"] = "None"
		}
		list = append(list, item)
	}
	return d.call(ctx, "Network.setCookies", map[string]any{"cookies": list}, nil)
}

// Cookies returns the cookies the page would send to url.
func (d *Driver) Cookies(ctx context.Context, url string) ([]*http.Cookie, error) {
	var r struct {
		Cookies []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
			SameSite string  `json:"sameSite"`
		} `json:"cookies"`
	}
	if err := d.call(ctx, "Network.getCookies", map[string]any{"urls": []string{url}}, &r); err != nil {
		return nil, err
	}
	out := make([]*http.Cookie, 0, len(r.Cookies))
	for _, c := range r.Cookies {
		cookie := &http.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, HttpOnly: c.HTTPOnly, Secure: c.Secure}
		if c.Expires > 0 {
			cookie.Expires = time.Unix(int64(c.Expires), 0)
		}
		switch c.SameSite {
		case "Lax":
			cookie.SameSite = http.SameSiteLaxMode
		case "Strict":
			cookie.SameSite = http.SameSiteStrictMode
		case "None":
			cookie.SameSite = http.SameSiteNoneMode
		}
		out = append(out, cookie)
	}
	return out, nil
}

// Close asks the browser to quit, kills it if it does not, and removes its
// profile directory.
func (d *Driver) Close() error {
	d.closeOnce.Do(func() {
		if d.conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = d.conn.call(ctx, "", "Browser.close", nil, nil)
			cancel()
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-d.exited:
		case <-timer.C:
			_ = d.cmd.Process.Kill()
			<-d.exited
		}
		timer.Stop()
		if w, ok := d.conn.w.(*os.File); ok {
			_ = w.Close()
		}
		if err := os.RemoveAll(d.dir); err != nil {
			d.closeErr = fmt.Errorf("cdp: remove the profile directory: %w", err)
		}
	})
	return d.closeErr
}

// tail keeps the last few kilobytes the browser wrote to stderr.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailSize = 4 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailSize {
		t.buf = t.buf[len(t.buf)-tailSize:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.ToValidUTF8(t.buf, nil))
}

// profileDir is the directory the browser keeps its profile in, for tests.
func (d *Driver) profileDir() string { return filepath.Clean(d.dir) }
