package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/test/trace"
)

// Page is one browser page under a test, on top of any Driver. It speaks as
// the person using the application does: visit, click the button named Save,
// fill the field labelled Email, expect the text.
//
// Every action waits before it acts and after: for its element to be the only
// visible match, enabled, still and not covered; and for the page to settle --
// the document loaded, htmx quiet (every request settled or ended with nothing
// to swap), the network idle and animations finished. There is no sleep to
// add, and no wait to call.
//
// The page fails the test by itself on an exception the page did not catch, a
// console.error, a response of 500 or more, and a Content-Security-Policy
// violation. Allow names the ones a test expects.
//
// Every step is recorded -- a PNG and the DOM after it, the network and the
// console -- and written in the trace format when the test fails, or always
// with ARANDU_E2E_TRACE=1.
type Page struct {
	t        testing.TB
	d        Driver
	info     Info
	rec      *trace.Recorder
	base     string
	routes   Resolver
	timeout  time.Duration
	quiet    time.Duration
	viewport Viewport

	mu         sync.Mutex
	inflight   map[string]string
	navigating bool
	netAt      time.Time
	pending    []trace.Failure
	allowed    []string
	changed    chan struct{}
	url        string
}

// Resolver turns a route name and its parameters into a path or a URL. The
// application's router provides it, so a test visits "invoices.show" and not a
// path it would have to keep in step with the routes.
type Resolver func(name string, params ...any) (string, error)

// Option configures a Page.
type Option func(*Page)

// BaseURL is the address relative paths are visited against, such as the
// address of the server the test started.
func BaseURL(url string) Option {
	return func(p *Page) { p.base = strings.TrimRight(url, "/") }
}

// Routes is how a route name becomes a URL. Without it, Visit and ExpectURL
// accept paths and URLs only.
func Routes(r Resolver) Option { return func(p *Page) { p.routes = r } }

// Timeout bounds every step: an action or an expectation that is not done by
// then fails, saying what it was still waiting for. The default is five
// seconds.
func Timeout(d time.Duration) Option { return func(p *Page) { p.timeout = d } }

// WithViewport is the viewport the page opens with. The default is 1440x900.
func WithViewport(v Viewport) Option { return func(p *Page) { p.viewport = v } }

// WithTrace records into r instead of a recorder of the page's own, so
// whatever else the test runs -- the server, above all -- can add to the same
// trace.
func WithTrace(r *trace.Recorder) Option { return func(p *Page) { p.rec = r } }

const (
	defaultTimeout = 5 * time.Second
	defaultQuiet   = 100 * time.Millisecond
	pollInterval   = 25 * time.Millisecond
	captureTimeout = 5 * time.Second
)

// Open launches the driver ARANDU_E2E_DRIVER picks from drivers, with the
// options the environment sets, and opens a page on it. The driver is closed
// when the test ends.
func Open(t testing.TB, drivers Registry, opts ...Option) *Page {
	t.Helper()
	_, factory, err := drivers.Select()
	if err != nil {
		t.Fatalf("%v", err)
	}
	launch, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return New(t, Launch(t, factory, launch), opts...)
}

// New opens a page on a driver already running. The page installs its script
// and listens to the driver; call it once per driver.
func New(t testing.TB, d Driver, opts ...Option) *Page {
	t.Helper()
	p := &Page{
		t:        t,
		d:        d,
		info:     d.Info(),
		timeout:  defaultTimeout,
		quiet:    defaultQuiet,
		viewport: Viewport{Width: 1440, Height: 900},
		inflight: map[string]string{},
		changed:  make(chan struct{}),
		netAt:    time.Now(),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.rec == nil {
		p.rec = trace.NewRecorder(t.Name())
	}
	p.rec.SetDriver(p.info.Driver, p.info.Browser)
	d.Listen(p.event)
	t.Cleanup(p.finish)

	ctx, cancel := context.WithTimeout(t.Context(), p.timeout)
	defer cancel()
	if err := d.AddInitScript(ctx, pageScript); err != nil {
		t.Fatalf("browser: install the page script: %v", err)
	}
	if err := d.SetViewport(ctx, p.viewport); err != nil {
		t.Fatalf("browser: viewport: %v", err)
	}
	if err := d.EmulateMedia(ctx, Media{ColorScheme: "light", ReducedMotion: "no-preference"}); err != nil {
		t.Fatalf("browser: media: %v", err)
	}
	return p
}

// Driver returns the driver under the page, for the rare test that needs the
// contract itself. It is the interface and never a concrete driver: a test
// that reaches for one engine's API stops running on the others.
func (p *Page) Driver() Driver { return p.d }

// Trace returns the recorder the page writes, for adding the server's side.
func (p *Page) Trace() *trace.Recorder { return p.rec }

// Allow accepts the automatic failures whose text contains fragment, for a
// test about an error page or a script that is expected to complain.
func (p *Page) Allow(fragment string) *Page {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowed = append(p.allowed, fragment)
	return p
}

// URL returns the address of the document the last step ended on.
func (p *Page) URL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url
}

// event records what the driver reports. It runs on the driver's goroutine, so
// it only records: failing the test happens on the test's goroutine, at the
// end of the step.
func (p *Page) event(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e.Kind {
	case EventConsole:
		p.rec.Console(trace.Message{Time: e.Time, Level: e.Level, Source: e.Source, Text: e.Text, URL: e.URL})
		if e.Source == "console-api" && e.Level == "error" {
			if strings.HasPrefix(e.Text, cspPrefix) {
				p.fail(e.Time, "csp", "Content-Security-Policy "+strings.TrimPrefix(e.Text, "arandu: content security policy "))
			} else {
				p.fail(e.Time, "console-error", "console.error: "+e.Text)
			}
		}
	case EventException:
		p.rec.Console(trace.Message{Time: e.Time, Level: "error", Source: "exception", Text: e.Text, URL: e.URL})
		p.fail(e.Time, "exception", "uncaught exception: "+e.Text)
	case EventRequest:
		if e.ResourceType != "EventSource" && e.ResourceType != "WebSocket" {
			p.inflight[e.RequestID] = e.ResourceType
		}
		p.netAt = e.Time
		p.rec.Request(trace.Request{ID: e.RequestID, Method: e.Method, URL: e.URL, Type: e.ResourceType, Started: e.Time})
	case EventResponse:
		p.rec.Request(trace.Request{ID: e.RequestID, URL: e.URL, Status: e.Status, ServerRequestID: e.Header.Get("X-Request-ID")})
		if e.Status >= 500 {
			method := e.Method
			if method == "" {
				method = "request to"
			}
			p.fail(e.Time, "server-error", fmt.Sprintf("%s %s answered %d", method, e.URL, e.Status))
		}
	case EventRequestFinished, EventRequestFailed:
		delete(p.inflight, e.RequestID)
		p.netAt = e.Time
		q := trace.Request{ID: e.RequestID, Finished: e.Time}
		if e.Kind == EventRequestFailed {
			q.Error = e.Text
			if q.Error == "" {
				q.Error = "failed"
			}
		}
		p.rec.Request(q)
	case EventNavigationStarted:
		p.navigating = true
		p.netAt = e.Time
	case EventNavigationFinished:
		p.navigating = false
		p.netAt = e.Time
	}
	close(p.changed)
	p.changed = make(chan struct{})
}

// fail queues an automatic failure. The caller holds p.mu.
func (p *Page) fail(at time.Time, kind, text string) {
	f := trace.Failure{Time: at, Kind: kind, Text: text}
	p.pending = append(p.pending, f)
	p.rec.Fail(f)
}

// failures takes the automatic failures queued since the last call, leaving
// out what Allow accepted.
func (p *Page) failures() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, f := range p.pending {
		allowed := false
		for _, fragment := range p.allowed {
			if strings.Contains(f.Text, fragment) {
				allowed = true
				break
			}
		}
		if !allowed {
			out = append(out, f.Text)
		}
	}
	p.pending = nil
	return out
}

// step runs one action or expectation under the step timeout, records it with
// what the page looked like after it, and fails the test on its error or on
// any automatic failure that arrived meanwhile.
func (p *Page) step(action, target string, fn func(ctx context.Context) error) {
	p.t.Helper()
	p.stepData(action, target, nil, fn)
}

// stepData is step, keeping what *data holds after fn as the step's data.
func (p *Page) stepData(action, target string, data *json.RawMessage, fn func(ctx context.Context) error) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), p.timeout)
	start := time.Now()
	err := fn(ctx)
	cancel()
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("%w (waited %s)", err, p.timeout)
	}
	url, png, dom := p.capture()
	s := trace.Step{Action: action, Target: target, URL: url, Started: start, Duration: time.Since(start)}
	if data != nil {
		s.Data = *data
	}
	if err != nil {
		s.Error = err.Error()
	}
	p.rec.Step(s, png, dom)
	p.report(action, target, err)
}

// report fails the test with the step's error and the automatic failures, the
// automatic ones first: a 500 is the cause of the text that did not appear.
func (p *Page) report(action, target string, err error) {
	p.t.Helper()
	found := p.failures()
	if len(found) == 0 && err == nil {
		return
	}
	var b strings.Builder
	if len(found) > 0 {
		b.WriteString("browser: the page failed: " + strings.Join(found, "; "))
	}
	if err != nil {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("browser: " + action)
		if target != "" {
			b.WriteString(" " + target)
		}
		b.WriteString(": " + err.Error())
	}
	p.t.Fatalf("%s", b.String())
}

// capture takes the URL, a PNG and the DOM after a step. Each part is best
// effort: a page in the middle of a navigation has no DOM to give.
func (p *Page) capture() (string, []byte, []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
	defer cancel()
	var snap struct {
		URL string `json:"url"`
		DOM string `json:"dom"`
	}
	_ = p.d.Evaluate(ctx, `({url: location.href, dom: window.__arandu ? window.__arandu.dom() : document.documentElement.outerHTML})`, &snap)
	png, _ := p.d.Screenshot(ctx)
	if snap.URL != "" {
		p.mu.Lock()
		p.url = snap.URL
		p.mu.Unlock()
	}
	var dom []byte
	if snap.DOM != "" {
		dom = []byte(snap.DOM)
	}
	return snap.URL, png, dom
}

// finish runs when the test ends: it reports the automatic failures no step
// reported, and writes the trace when the test failed or one was asked for.
func (p *Page) finish() {
	if found := p.failures(); len(found) > 0 {
		p.t.Errorf("browser: the page failed after the last step: %s", strings.Join(found, "; "))
	}
	failed := p.t.Failed()
	if !failed && !trace.Always() {
		return
	}
	url, png, dom := p.capture()
	p.rec.Step(trace.Step{Action: "end", URL: url, Started: time.Now()}, png, dom)
	dir := trace.Dir(p.t.Name())
	if err := p.rec.Write(dir, failed); err != nil {
		p.t.Errorf("browser: %v", err)
		return
	}
	p.t.Logf("browser: trace written to %s", dir)
}

// changes returns the channel closed by the next event.
func (p *Page) changes() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

// pause waits for the next event, for d, or for ctx, whichever comes first. It
// is how every wait here polls: a page that reports something is looked at
// again at once, a quiet one every d.
func (p *Page) pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-p.changes():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// networkBusy says why the network is not idle, or "" when it is.
func (p *Page) networkBusy() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.navigating:
		return "a navigation is loading"
	case len(p.inflight) > 0:
		return fmt.Sprintf("%d request(s) in flight", len(p.inflight))
	case time.Since(p.netAt) < p.quiet:
		return "the network was active " + time.Since(p.netAt).Round(time.Millisecond).String() + " ago"
	}
	return ""
}

type pageState struct {
	Doc       string `json:"doc"`
	Ready     string `json:"ready"`
	HTMX      bool   `json:"htmx"`
	Animating bool   `json:"animating"`
	URL       string `json:"url"`
	Leaving   int64  `json:"leaving"`
}

const stateExpression = `(window.__arandu ? window.__arandu.state() : {doc: '', ready: document.readyState, htmx: true, animating: false, url: location.href, leaving: -1})`

// settle waits until the page is still: the document complete and not being
// left, htmx quiet, the network idle and no finite animation running.
func (p *Page) settle(ctx context.Context) error {
	waiting := ""
	for {
		if busy := p.networkBusy(); busy != "" {
			waiting = busy
		} else {
			var s pageState
			err := p.d.Evaluate(ctx, stateExpression, &s)
			switch {
			case errors.Is(err, ErrDocumentChanged):
				waiting = "the document was being replaced"
			case err != nil && ctx.Err() == nil:
				return err
			case err != nil:
			case s.Ready != "complete":
				waiting = "the document is " + s.Ready
			case s.Leaving >= 0 && s.Leaving < 2000:
				waiting = "the document is being left for another"
			case !s.HTMX:
				waiting = "htmx requests have not settled"
			case s.Animating:
				waiting = "animations are running"
			default:
				p.mu.Lock()
				p.url = s.URL
				p.mu.Unlock()
				return nil
			}
		}
		if err := p.pause(ctx, pollInterval); err != nil {
			return fmt.Errorf("the page did not settle: %s", waiting)
		}
	}
}

type found struct {
	Status      string  `json:"status"`
	Detail      string  `json:"detail"`
	Description string  `json:"description"`
	Text        string  `json:"text"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
}

// find waits until the locator resolves to one element ready for action:
// "click", "fill", "select", "check", "visible" or "hidden".
func (p *Page) find(ctx context.Context, l Locator, action string) (found, error) {
	if problem := l.problem(); problem != "" {
		return found{}, errors.New(problem)
	}
	encoded, err := json.Marshal(l)
	if err != nil {
		return found{}, err
	}
	expr := fmt.Sprintf(`(window.__arandu ? window.__arandu.find(%s, %q) : {status: 'none', detail: 'the page script is not installed in this document; visit a page first'})`, encoded, action)
	waiting := ""
	for {
		var f found
		err := p.d.Evaluate(ctx, expr, &f)
		switch {
		case errors.Is(err, ErrDocumentChanged):
			waiting = "the document was being replaced"
		case err != nil && ctx.Err() == nil:
			return found{}, err
		case err != nil:
		case f.Status == "ok":
			return f, nil
		case f.Status == "many":
			return found{}, errors.New(f.Detail)
		default:
			waiting = f.Detail
		}
		if err := p.pause(ctx, pollInterval); err != nil {
			if waiting == "" {
				waiting = "no answer from the page"
			}
			return found{}, errors.New(waiting)
		}
	}
}

// call evaluates one of the page script's functions and decodes its result.
func (p *Page) call(ctx context.Context, expr string, out any) error {
	return p.d.Evaluate(ctx, "window.__arandu."+expr, out)
}

// resolve turns what Visit and ExpectURL accept into a URL: a URL as it is, a
// path against BaseURL, anything else as a route name through Routes.
func (p *Page) resolve(target string, params []any) (string, error) {
	switch {
	case strings.Contains(target, "://") || strings.HasPrefix(target, "about:") || strings.HasPrefix(target, "data:"):
		return target, nil
	case strings.HasPrefix(target, "/"):
		if p.base == "" {
			return "", fmt.Errorf("%q is a path and the page has no BaseURL to visit it against", target)
		}
		return p.base + target, nil
	}
	if p.routes == nil {
		return "", fmt.Errorf("%q reads as a route name and the page was given no Routes resolver; pass browser.Routes, or a path that starts with /", target)
	}
	resolved, err := p.routes(target, params...)
	if err != nil {
		return "", fmt.Errorf("route %q: %w", target, err)
	}
	if strings.HasPrefix(resolved, "/") {
		if p.base == "" {
			return "", fmt.Errorf("route %q resolved to the path %q and the page has no BaseURL", target, resolved)
		}
		return p.base + resolved, nil
	}
	return resolved, nil
}

// Visit loads a page: a URL, a path against BaseURL, or a route name with its
// parameters through Routes.
//
//	page.Visit("/invoices")
//	page.Visit("invoices.show", invoice.ID)
func (p *Page) Visit(target string, params ...any) {
	p.t.Helper()
	p.step("visit", target, func(ctx context.Context) error {
		url, err := p.resolve(target, params)
		if err != nil {
			return err
		}
		if err := p.d.Navigate(ctx, url); err != nil {
			return err
		}
		return p.settle(ctx)
	})
}

// Click clicks the element the locator finds with the pointer, at the centre
// of its first box, once it is the only visible match, enabled, still and not
// covered by anything.
func (p *Page) Click(l Locator) {
	p.t.Helper()
	p.step("click", l.String(), func(ctx context.Context) error { return p.click(ctx, l) })
}

func (p *Page) click(ctx context.Context, l Locator) error {
	if err := p.settle(ctx); err != nil {
		return err
	}
	f, err := p.find(ctx, l, "click")
	if err != nil {
		return err
	}
	if err := p.d.Click(ctx, f.X, f.Y); err != nil {
		return err
	}
	return p.settle(ctx)
}

// ClickRole clicks the element with the role and accessible name.
func (p *Page) ClickRole(role, name string) {
	p.t.Helper()
	p.Click(Role(role, name))
}

// ClickText clicks the innermost element whose visible text is text.
func (p *Page) ClickText(text string) {
	p.t.Helper()
	p.Click(Text(text))
}

// Fill types value into the form control labelled label, replacing what was
// there: it clicks the control, selects its content and types.
func (p *Page) Fill(label, value string) {
	p.t.Helper()
	p.FillIn(Label(label), value)
}

// FillIn types value into the control the locator finds.
func (p *Page) FillIn(l Locator, value string) {
	p.t.Helper()
	p.step("fill", l.String(), func(ctx context.Context) error { return p.fill(ctx, l, value) })
}

func (p *Page) fill(ctx context.Context, l Locator, value string) error {
	if err := p.settle(ctx); err != nil {
		return err
	}
	f, err := p.find(ctx, l, "fill")
	if err != nil {
		return err
	}
	if err := p.d.Click(ctx, f.X, f.Y); err != nil {
		return err
	}
	var focus struct {
		OK     bool   `json:"ok"`
		Native bool   `json:"native"`
		Detail string `json:"detail"`
	}
	if err := p.call(ctx, "focusTarget()", &focus); err != nil {
		return err
	}
	if !focus.OK {
		return fmt.Errorf("%s did not take the focus: %s", f.Description, focus.Detail)
	}
	switch {
	case focus.Native:
		// A date, time, colour or range input takes no typed text.
		quoted, _ := json.Marshal(value)
		if err := p.call(ctx, "setTargetValue("+string(quoted)+")", nil); err != nil {
			return err
		}
	case value == "":
		if err := p.d.Press(ctx, "Delete"); err != nil {
			return err
		}
	default:
		if err := p.d.InsertText(ctx, value); err != nil {
			return err
		}
	}
	return p.settle(ctx)
}

// Select picks the option whose text, or failing that whose value, is option
// in the select labelled label.
func (p *Page) Select(label, option string) {
	p.t.Helper()
	p.SelectIn(Label(label), option)
}

// SelectIn picks the option in the select the locator finds.
func (p *Page) SelectIn(l Locator, option string) {
	p.t.Helper()
	p.step("select", l.String()+" option="+option, func(ctx context.Context) error {
		if err := p.settle(ctx); err != nil {
			return err
		}
		if _, err := p.find(ctx, l, "select"); err != nil {
			return err
		}
		quoted, _ := json.Marshal(option)
		var r struct {
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		}
		if err := p.call(ctx, "selectOption("+string(quoted)+")", &r); err != nil {
			return err
		}
		if !r.OK {
			return errors.New(r.Detail)
		}
		return p.settle(ctx)
	})
}

// Check ticks the checkbox or radio labelled label, clicking it -- or its
// label, when the control itself is hidden behind a styled one -- unless it is
// ticked already.
func (p *Page) Check(label string) {
	p.t.Helper()
	p.CheckIn(Label(label), true)
}

// Uncheck clears the checkbox labelled label.
func (p *Page) Uncheck(label string) {
	p.t.Helper()
	p.CheckIn(Label(label), false)
}

// CheckIn sets the checkbox or radio the locator finds to want.
func (p *Page) CheckIn(l Locator, want bool) {
	p.t.Helper()
	action := "check"
	if !want {
		action = "uncheck"
	}
	p.step(action, l.String(), func(ctx context.Context) error {
		if err := p.settle(ctx); err != nil {
			return err
		}
		f, err := p.find(ctx, l, "check")
		if err != nil {
			return err
		}
		var have bool
		if err := p.call(ctx, "controlChecked()", &have); err != nil {
			return err
		}
		if have == want {
			return nil
		}
		if err := p.d.Click(ctx, f.X, f.Y); err != nil {
			return err
		}
		if err := p.settle(ctx); err != nil {
			return err
		}
		if err := p.call(ctx, "controlChecked()", &have); err != nil {
			return err
		}
		if have != want {
			return fmt.Errorf("clicking %s left it checked=%t", f.Description, have)
		}
		return nil
	})
}

// Submit submits the form of the field filled last, or the only form on the
// page, the way pressing Enter in it does: the submit event fires, so htmx and
// validation see it.
func (p *Page) Submit() {
	p.t.Helper()
	p.step("submit", "", func(ctx context.Context) error {
		if err := p.settle(ctx); err != nil {
			return err
		}
		var r struct {
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		}
		if err := p.call(ctx, "submit()", &r); err != nil && !errors.Is(err, ErrDocumentChanged) {
			return err
		}
		if !r.OK && r.Detail != "" {
			return errors.New(r.Detail)
		}
		return p.settle(ctx)
	})
}

// Press presses a key in the focused element: "Enter", "Tab", "Escape",
// "ArrowDown", a character, or a chord such as "Shift+Tab".
func (p *Page) Press(key string) {
	p.t.Helper()
	p.step("press", key, func(ctx context.Context) error {
		if err := p.settle(ctx); err != nil {
			return err
		}
		if err := p.d.Press(ctx, key); err != nil {
			return err
		}
		return p.settle(ctx)
	})
}

// poll evaluates check until it reports done, the page changing or not, and
// fails with the last explanation when the step runs out of time.
func (p *Page) poll(ctx context.Context, check func(ctx context.Context) (bool, string, error)) error {
	waiting := ""
	for {
		done, why, err := check(ctx)
		switch {
		case errors.Is(err, ErrDocumentChanged):
			waiting = "the document was being replaced"
		case err != nil && ctx.Err() == nil:
			return err
		case err != nil:
		case done:
			return nil
		default:
			waiting = why
		}
		if err := p.pause(ctx, pollInterval); err != nil {
			return errors.New(waiting)
		}
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// ExpectText waits until text is part of what the page reads, whitespace
// collapsed.
func (p *Page) ExpectText(text string) {
	p.t.Helper()
	want := normalize(text)
	p.step("expect text", text, func(ctx context.Context) error {
		return p.poll(ctx, func(ctx context.Context) (bool, string, error) {
			var have string
			err := p.call(ctx, "text()", &have)
			return strings.Contains(have, want), fmt.Sprintf("%q is not on the page, which reads: %q", text, clip(have, 300)), err
		})
	})
}

// ExpectNoText waits until text is not part of what the page reads.
func (p *Page) ExpectNoText(text string) {
	p.t.Helper()
	want := normalize(text)
	p.step("expect no text", text, func(ctx context.Context) error {
		return p.poll(ctx, func(ctx context.Context) (bool, string, error) {
			var have string
			err := p.call(ctx, "text()", &have)
			return !strings.Contains(have, want), fmt.Sprintf("%q is still on the page", text), err
		})
	})
}

// ExpectURL waits until the page is at target: a URL compared whole, a path
// compared with the path (and the query, when target has one), or a route
// name with its parameters through Routes.
func (p *Page) ExpectURL(target string, params ...any) {
	p.t.Helper()
	p.step("expect url", target, func(ctx context.Context) error {
		want, err := p.resolve(target, params)
		if err != nil {
			return err
		}
		if p.base != "" && strings.HasPrefix(want, p.base+"/") && !strings.Contains(target, "://") {
			want = strings.TrimPrefix(want, p.base)
		}
		return p.poll(ctx, func(ctx context.Context) (bool, string, error) {
			var have struct {
				Href   string `json:"href"`
				Path   string `json:"path"`
				Search string `json:"search"`
			}
			err := p.d.Evaluate(ctx, `({href: location.href, path: location.pathname, search: location.search})`, &have)
			got := have.Href
			if strings.HasPrefix(want, "/") {
				got = have.Path
				if strings.Contains(want, "?") {
					got += have.Search
				}
			}
			return got == want, fmt.Sprintf("the page is at %s, not %s", have.Href, want), err
		})
	})
}

// ExpectVisible waits until the locator finds exactly one visible element.
func (p *Page) ExpectVisible(l Locator) {
	p.t.Helper()
	p.step("expect visible", l.String(), func(ctx context.Context) error {
		_, err := p.find(ctx, l, "visible")
		return err
	})
}

// ExpectHidden waits until the locator finds no visible element.
func (p *Page) ExpectHidden(l Locator) {
	p.t.Helper()
	p.step("expect hidden", l.String(), func(ctx context.Context) error {
		_, err := p.find(ctx, l, "hidden")
		return err
	})
}

// ExpectModal waits until a modal dialog named name is open, and returns it
// for acting inside:
//
//	page.ClickRole("button", "Delete")
//	page.ExpectModal("Delete invoice").ClickRole("button", "Delete")
func (p *Page) ExpectModal(name string) *Element {
	p.t.Helper()
	l := Modal(name)
	p.ExpectVisible(l)
	return &Element{p: p, l: l}
}

// Find returns the element the locator names, for acting on or inside it.
// Nothing is looked up until an action or an expectation runs.
func (p *Page) Find(l Locator) *Element { return &Element{p: p, l: l} }

// Screenshot records the page as it is, under name, in the trace.
func (p *Page) Screenshot(name string) {
	p.t.Helper()
	p.step("screenshot", name, func(ctx context.Context) error { return p.settle(ctx) })
}

// Evaluate runs a JavaScript expression in the page and decodes its value
// into out, which may be nil. It is for reading what no expectation reads;
// acting through it skips everything a person's action goes through.
func (p *Page) Evaluate(expression string, out any) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), p.timeout)
	defer cancel()
	if err := p.d.Evaluate(ctx, expression, out); err != nil {
		p.t.Fatalf("browser: evaluate: %v", err)
	}
}

// SetCookies stores cookies for BaseURL in the page's browsing context, as a
// response from the server would have.
func (p *Page) SetCookies(cookies ...*http.Cookie) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), p.timeout)
	defer cancel()
	if p.base == "" {
		p.t.Fatalf("browser: SetCookies needs a BaseURL to set the cookies for")
	}
	if err := p.d.SetCookies(ctx, p.base, cookies); err != nil {
		p.t.Fatalf("browser: set cookies: %v", err)
	}
}

// Pause stops the test with the browser open, when ARANDU_E2E_HEADED shows
// it, until window.__arandu.resume() is called in the page's DevTools
// console. Without a window to look at, or in CI, it does nothing.
func (p *Page) Pause() {
	p.t.Helper()
	if !truthy(os.Getenv("ARANDU_E2E_HEADED")) || os.Getenv("CI") != "" {
		p.t.Logf("browser: Pause ignored; it waits only with ARANDU_E2E_HEADED=1 and outside CI")
		return
	}
	p.t.Logf("browser: paused at %s; run window.__arandu.resume() in the page's DevTools console to go on", p.URL())
	ctx := p.t.Context()
	for {
		var resumed bool
		err := p.call(ctx, "resumed()", &resumed)
		if err == nil && resumed {
			return
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

// Element is what a locator finds, for acting on it or inside it.
type Element struct {
	p *Page
	l Locator
}

// Locator returns the element's locator.
func (e *Element) Locator() Locator { return e.l }

// Find returns the element child finds inside this one.
func (e *Element) Find(child Locator) *Element { return &Element{p: e.p, l: child.In(e.l)} }

// Click clicks the element.
func (e *Element) Click() { e.p.t.Helper(); e.p.Click(e.l) }

// ClickRole clicks the element with the role and name inside this one.
func (e *Element) ClickRole(role, name string) { e.p.t.Helper(); e.p.Click(Role(role, name).In(e.l)) }

// ClickText clicks the element with the text inside this one.
func (e *Element) ClickText(text string) { e.p.t.Helper(); e.p.Click(Text(text).In(e.l)) }

// Fill types value into the element.
func (e *Element) Fill(value string) { e.p.t.Helper(); e.p.FillIn(e.l, value) }

// Select picks an option of the element.
func (e *Element) Select(option string) { e.p.t.Helper(); e.p.SelectIn(e.l, option) }

// Check ticks the element.
func (e *Element) Check() { e.p.t.Helper(); e.p.CheckIn(e.l, true) }

// Uncheck clears the element.
func (e *Element) Uncheck() { e.p.t.Helper(); e.p.CheckIn(e.l, false) }

// ExpectVisible waits until the element is visible.
func (e *Element) ExpectVisible() { e.p.t.Helper(); e.p.ExpectVisible(e.l) }

// ExpectHidden waits until the element is not visible.
func (e *Element) ExpectHidden() { e.p.t.Helper(); e.p.ExpectHidden(e.l) }

// ExpectText waits until the element's visible text contains text.
func (e *Element) ExpectText(text string) {
	e.p.t.Helper()
	want := normalize(text)
	e.p.step("expect text", e.l.String()+" text="+text, func(ctx context.Context) error {
		return e.p.poll(ctx, func(ctx context.Context) (bool, string, error) {
			f, err := e.p.find(ctx, e.l, "visible")
			if err != nil {
				return false, err.Error(), nil
			}
			return strings.Contains(f.Text, want), fmt.Sprintf("%s reads %q, not %q", f.Description, clip(f.Text, 200), text), nil
		})
	})
}
