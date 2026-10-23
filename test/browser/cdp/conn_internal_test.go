package cdp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/test/browser"
)

// fakeBrowser is the far end of the debugging pipe: it reads the commands the
// connection writes and answers them as Chrome would, NUL framing included.
type fakeBrowser struct {
	t   *testing.T
	in  *bufio.Reader
	out *os.File
	mu  sync.Mutex
}

func newFake(t *testing.T, closing func(error) error) (*conn, *fakeBrowser) {
	t.Helper()
	browserReads, connWrites, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	connReads, browserWrites, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// A command that never arrives whole fails the test instead of hanging it.
	if err := browserReads.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, f := range []*os.File{browserReads, connWrites, connReads, browserWrites} {
			_ = f.Close()
		}
	})
	return newConn(connReads, connWrites, closing), &fakeBrowser{t: t, in: bufio.NewReader(browserReads), out: browserWrites}
}

// next reads the next command.
func (f *fakeBrowser) next() message {
	frame, err := f.in.ReadBytes(0)
	if err != nil {
		f.t.Errorf("fake browser: read: %v", err)
		return message{}
	}
	var m message
	if err := json.Unmarshal(frame[:len(frame)-1], &m); err != nil {
		f.t.Errorf("fake browser: the command is not JSON: %q", frame)
	}
	return m
}

func (f *fakeBrowser) send(m any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	encoded, err := json.Marshal(m)
	if err != nil {
		f.t.Error(err)
		return
	}
	if _, err := f.out.Write(append(encoded, 0)); err != nil {
		f.t.Errorf("fake browser: write: %v", err)
	}
}

func (f *fakeBrowser) reply(cmd message, result any) {
	f.send(map[string]any{"id": cmd.ID, "sessionId": cmd.SessionID, "result": result})
}

func (f *fakeBrowser) refuse(cmd message, code int, text, data string) {
	f.send(map[string]any{"id": cmd.ID, "sessionId": cmd.SessionID, "error": map[string]any{"code": code, "message": text, "data": data}})
}

func (f *fakeBrowser) event(session, method string, params any) {
	f.send(map[string]any{"sessionId": session, "method": method, "params": params})
}

// serve answers commands with answer until the pipe closes.
func (f *fakeBrowser) serve(answer func(m message)) {
	go func() {
		for {
			frame, err := f.in.ReadBytes(0)
			if err != nil {
				return
			}
			var m message
			if json.Unmarshal(frame[:len(frame)-1], &m) == nil {
				answer(m)
			}
		}
	}()
}

func TestCallsAreFramedAndRepliesRoutedById(t *testing.T) {
	c, fake := newFake(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		out string
		err error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		var r struct{ Product string }
		err := c.call(ctx, "", "Browser.getVersion", nil, &r)
		first <- result{r.Product, err}
	}()
	a := fake.next()
	go func() {
		var r struct{ Value string }
		err := c.call(ctx, "S1", "Runtime.evaluate", map[string]any{"expression": "1"}, &r)
		second <- result{r.Value, err}
	}()
	b := fake.next()
	if a.Method != "Browser.getVersion" || a.SessionID != "" || b.Method != "Runtime.evaluate" || b.SessionID != "S1" || a.ID == b.ID {
		t.Fatalf("commands = %+v, %+v", a, b)
	}
	if string(b.Params) != `{"expression":"1"}` {
		t.Fatalf("params = %s", b.Params)
	}
	// Answered out of order: each reply reaches its own caller.
	fake.reply(b, map[string]string{"Value": "evaluated"})
	fake.reply(a, map[string]string{"Product": "Chrome/155"})
	if r := <-first; r.err != nil || r.out != "Chrome/155" {
		t.Fatalf("first = %+v", r)
	}
	if r := <-second; r.err != nil || r.out != "evaluated" {
		t.Fatalf("second = %+v", r)
	}
}

func TestARefusedCommandCarriesChromesText(t *testing.T) {
	c, fake := newFake(t, nil)
	fake.serve(func(m message) { fake.refuse(m, -32000, "Cannot navigate to invalid URL", "about:nothing") })
	err := c.call(context.Background(), "S", "Page.navigate", nil, nil)
	want := "cdp: Page.navigate: Cannot navigate to invalid URL (about:nothing)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestEventsReachOnlyTheirSession(t *testing.T) {
	c, fake := newFake(t, nil)
	got := make(chan string, 4)
	c.listen("A", func(method string, params json.RawMessage) { got <- "A " + method })
	c.listen("B", func(method string, params json.RawMessage) { got <- "B " + method })
	fake.event("B", "Page.loadEventFired", map[string]any{})
	fake.event("C", "Page.loadEventFired", map[string]any{})
	fake.event("A", "Runtime.consoleAPICalled", map[string]any{})
	if first, second := <-got, <-got; first != "B Page.loadEventFired" || second != "A Runtime.consoleAPICalled" {
		t.Fatalf("routed = %q, %q", first, second)
	}
	select {
	case extra := <-got:
		t.Fatalf("an event of another session was delivered: %q", extra)
	default:
	}
}

func TestACallGivesUpAtItsDeadline(t *testing.T) {
	c, fake := newFake(t, nil)
	fake.serve(func(message) {}) // reads every command and answers none
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := c.call(ctx, "S", "Runtime.evaluate", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "Runtime.evaluate") {
		t.Fatalf("err = %v", err)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d abandoned call(s) still pending", pending)
	}
}

func TestAClosedPipeFailsEveryCallWithTheReason(t *testing.T) {
	c, fake := newFake(t, func(err error) error { return fmt.Errorf("%w; the browser wrote: no display", err) })
	done := make(chan error, 1)
	go func() { done <- c.call(context.Background(), "S", "Page.enable", nil, nil) }()
	fake.next()
	_ = fake.out.Close()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "closed the debugging pipe; the browser wrote: no display") {
		t.Fatalf("pending call: %v", err)
	}
	if err := c.call(context.Background(), "S", "Page.enable", nil, nil); err == nil || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("later call: %v", err)
	}
}

// fakeDriver is a Driver attached to a fake browser, skipping the launch.
func fakeDriver(t *testing.T) (*Driver, *fakeBrowser, chan browser.Event) {
	t.Helper()
	c, fake := newFake(t, nil)
	d := &Driver{conn: c, session: "S", frame: "MAIN", loads: make(chan string, 32), methods: map[string]string{}}
	c.listen("S", d.handle)
	events := make(chan browser.Event, 32)
	d.Listen(func(e browser.Event) { events <- e })
	return d, fake, events
}

func TestEventsAreTranslated(t *testing.T) {
	d, fake, events := fakeDriver(t)
	fake.event("S", "Runtime.consoleAPICalled", map[string]any{
		"type": "error",
		"args": []map[string]any{{"type": "string", "value": "failed"}, {"type": "number", "value": 42}, {"type": "object", "description": "Error: x"}},
	})
	fake.event("S", "Log.entryAdded", map[string]any{"entry": map[string]any{"source": "network", "level": "error", "text": "Failed to load resource"}})
	fake.event("S", "Runtime.exceptionThrown", map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "TypeError: boom"}}})
	fake.event("S", "Network.requestWillBeSent", map[string]any{"requestId": "1", "type": "Document", "request": map[string]any{"url": "http://a.test/", "method": "POST"}})
	fake.event("S", "Network.requestWillBeSent", map[string]any{"requestId": "1", "type": "Document", "request": map[string]any{"url": "http://a.test/next", "method": "GET"},
		"redirectResponse": map[string]any{"url": "http://a.test/", "status": 303, "headers": map[string]string{}}})
	fake.event("S", "Network.responseReceived", map[string]any{"requestId": "1", "type": "Document", "response": map[string]any{"url": "http://a.test/next", "status": 503, "headers": map[string]string{"x-request-id": "req-9"}}})
	fake.event("S", "Network.loadingFailed", map[string]any{"requestId": "2", "errorText": "net::ERR_ABORTED"})
	fake.event("S", "Network.loadingFinished", map[string]any{"requestId": "1"})
	fake.event("S", "Page.frameStartedLoading", map[string]any{"frameId": "IFRAME"})
	fake.event("S", "Page.frameStartedLoading", map[string]any{"frameId": "MAIN"})
	fake.event("S", "Page.frameStoppedLoading", map[string]any{"frameId": "MAIN"})

	want := []string{
		"console error console-api failed 42 Error: x",
		"console error network Failed to load resource",
		"exception TypeError: boom",
		"request POST http://a.test/ Document",
		"response POST http://a.test/ 303",
		"request GET http://a.test/next Document",
		"response GET http://a.test/next 503 req-9",
		"request-failed net::ERR_ABORTED",
		"request-finished 1",
		"navigation-started",
		"navigation-finished",
	}
	for i, w := range want {
		var e browser.Event
		select {
		case e = <-events:
		case <-time.After(5 * time.Second):
			t.Fatalf("event %d never came; want %q", i, w)
		}
		var got string
		switch e.Kind {
		case browser.EventConsole:
			got = strings.Join([]string{"console", e.Level, e.Source, e.Text}, " ")
		case browser.EventException:
			got = "exception " + e.Text
		case browser.EventRequest:
			got = strings.Join([]string{"request", e.Method, e.URL, e.ResourceType}, " ")
		case browser.EventResponse:
			got = fmt.Sprintf("response %s %s %d", e.Method, e.URL, e.Status)
			if id := e.Header.Get("X-Request-ID"); id != "" {
				got += " " + id
			}
		case browser.EventRequestFailed:
			got = "request-failed " + e.Text
		case browser.EventRequestFinished:
			got = "request-finished " + e.RequestID
		default:
			got = string(e.Kind)
		}
		if got != w {
			t.Fatalf("event %d = %q, want %q", i, got, w)
		}
		if e.Time.IsZero() {
			t.Fatalf("event %d has no time", i)
		}
	}
	_ = d
}

func TestNavigateWaitsForTheLoadOfItsOwnNavigation(t *testing.T) {
	d, fake, _ := fakeDriver(t)
	fake.serve(func(m message) {
		switch m.Method {
		case "Page.navigate":
			// A load from the navigation before arrives first; it is not ours.
			fake.event("S", "Page.lifecycleEvent", map[string]any{"frameId": "MAIN", "loaderId": "OLD", "name": "load"})
			fake.reply(m, map[string]any{"frameId": "MAIN", "loaderId": "NEW"})
			fake.event("S", "Page.lifecycleEvent", map[string]any{"frameId": "MAIN", "loaderId": "NEW", "name": "DOMContentLoaded"})
			fake.event("S", "Page.lifecycleEvent", map[string]any{"frameId": "IFRAME", "loaderId": "NEW", "name": "load"})
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := d.Navigate(ctx, "http://a.test/")
	if err == nil || !strings.Contains(err.Error(), "did not finish loading") {
		t.Fatalf("Navigate returned on a load that was not its own: %v", err)
	}

	fake2Driver, fake2, _ := fakeDriver(t)
	fake2.serve(func(m message) {
		fake2.reply(m, map[string]any{"frameId": "MAIN", "loaderId": "NEW"})
		fake2.event("S", "Page.lifecycleEvent", map[string]any{"frameId": "MAIN", "loaderId": "NEW", "name": "load"})
	})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := fake2Driver.Navigate(ctx2, "http://a.test/"); err != nil {
		t.Fatal(err)
	}
}

func TestNavigateReportsTheNetworkError(t *testing.T) {
	d, fake, _ := fakeDriver(t)
	fake.serve(func(m message) {
		fake.reply(m, map[string]any{"frameId": "MAIN", "loaderId": "L", "errorText": "net::ERR_CONNECTION_REFUSED"})
	})
	err := d.Navigate(context.Background(), "http://127.0.0.1:1/")
	if err == nil || err.Error() != "navigate to http://127.0.0.1:1/: net::ERR_CONNECTION_REFUSED" {
		t.Fatalf("err = %v", err)
	}
}

func TestEvaluate(t *testing.T) {
	d, fake, _ := fakeDriver(t)
	fake.serve(func(m message) {
		var p struct{ Expression string }
		_ = json.Unmarshal(m.Params, &p)
		switch p.Expression {
		case "value":
			fake.reply(m, map[string]any{"result": map[string]any{"type": "number", "value": 42}})
		case "throws":
			fake.reply(m, map[string]any{"result": map[string]any{"type": "object"}, "exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "Error: kaput\n    at <anonymous>:1:7"}}})
		case "navigating":
			fake.refuse(m, -32000, "Execution context was destroyed.", "")
		case "undefined":
			fake.reply(m, map[string]any{"result": map[string]any{"type": "undefined"}})
		}
	})
	ctx := context.Background()
	var n int
	if err := d.Evaluate(ctx, "value", &n); err != nil || n != 42 {
		t.Fatalf("value = %d, %v", n, err)
	}
	if err := d.Evaluate(ctx, "throws", nil); err == nil || !strings.HasPrefix(err.Error(), "javascript: Error: kaput") {
		t.Fatalf("throws = %v", err)
	}
	if err := d.Evaluate(ctx, "navigating", nil); !errors.Is(err, browser.ErrDocumentChanged) || !strings.Contains(err.Error(), "Execution context was destroyed") {
		t.Fatalf("navigating = %v", err)
	}
	if err := d.Evaluate(ctx, "undefined", &n); err == nil || !strings.Contains(err.Error(), "evaluated to undefined") {
		t.Fatalf("undefined into a value = %v", err)
	}
	if err := d.Evaluate(ctx, "undefined", nil); err != nil {
		t.Fatalf("undefined discarded = %v", err)
	}
}

func TestPressSendsWhatChromeNeeds(t *testing.T) {
	d, fake, _ := fakeDriver(t)
	sent := make(chan string, 16)
	fake.serve(func(m message) {
		sent <- string(m.Params)
		fake.reply(m, map[string]any{})
	})
	ctx := context.Background()
	for _, key := range []string{"Enter", "Shift+Tab", "a", "Control+a"} {
		if err := d.Press(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`{"code":"Enter","key":"Enter","modifiers":0,"text":"\r","type":"keyDown","unmodifiedText":"\r","windowsVirtualKeyCode":13}`,
		`{"code":"Enter","key":"Enter","modifiers":0,"type":"keyUp","windowsVirtualKeyCode":13}`,
		`{"code":"Tab","key":"Tab","modifiers":8,"type":"rawKeyDown","windowsVirtualKeyCode":9}`,
		`{"code":"Tab","key":"Tab","modifiers":8,"type":"keyUp","windowsVirtualKeyCode":9}`,
		`{"code":"KeyA","key":"a","modifiers":0,"text":"a","type":"keyDown","unmodifiedText":"a","windowsVirtualKeyCode":65}`,
		`{"code":"KeyA","key":"a","modifiers":0,"type":"keyUp","windowsVirtualKeyCode":65}`,
		`{"code":"KeyA","key":"a","modifiers":2,"type":"rawKeyDown","windowsVirtualKeyCode":65}`,
		`{"code":"KeyA","key":"a","modifiers":2,"type":"keyUp","windowsVirtualKeyCode":65}`,
	}
	for i, w := range want {
		if got := <-sent; got != w {
			t.Fatalf("command %d = %s\nwant          %s", i, got, w)
		}
	}
	if err := d.Press(ctx, "Hyper+a"); err == nil || !strings.Contains(err.Error(), `unknown modifier "Hyper"`) {
		t.Fatalf("unknown modifier = %v", err)
	}
	if err := d.Press(ctx, "Banana"); err == nil || !strings.Contains(err.Error(), `unknown key "Banana"`) {
		t.Fatalf("unknown key = %v", err)
	}
}

func TestFind(t *testing.T) {
	if _, err := find("", nil); !errors.Is(err, browser.ErrNoBrowser) {
		t.Fatalf("nothing anywhere = %v", err)
	}
	if _, err := find("", []string{"/nowhere/chrome", "no-such-browser-on-path"}); !errors.Is(err, browser.ErrNoBrowser) || !strings.Contains(err.Error(), "/nowhere/chrome, no-such-browser-on-path") {
		t.Fatalf("the error must name where it looked: %v", err)
	}
	if _, err := find("/nowhere/chrome", nil); err == nil || errors.Is(err, browser.ErrNoBrowser) {
		t.Fatalf("a CHROME_PATH that does not exist is a mistake to fail on, not a reason to skip: %v", err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "chromium")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := find("", []string{"/nowhere/chrome", exe}); err != nil || got != exe {
		t.Fatalf("found = %q, %v", got, err)
	}
	if got, err := find(exe, []string{"/nowhere/chrome"}); err != nil || got != exe {
		t.Fatalf("CHROME_PATH = %q, %v", got, err)
	}
}

func TestLaunchRefusesAnotherEngine(t *testing.T) {
	_, err := Launch(context.Background(), browser.Options{Browser: "firefox"})
	if err == nil || !strings.Contains(err.Error(), `asks for "firefox"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestLaunchFailsOnABrokenChromePath(t *testing.T) {
	t.Setenv("CHROME_PATH", filepath.Join(t.TempDir(), "missing"))
	_, err := Launch(context.Background(), browser.Options{})
	if err == nil || errors.Is(err, browser.ErrNoBrowser) || !strings.Contains(err.Error(), "CHROME_PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestStderrKeepsOnlyTheTail(t *testing.T) {
	var tl tail
	_, _ = tl.Write([]byte(strings.Repeat("x", tailSize)))
	_, _ = tl.Write([]byte("the reason"))
	if s := tl.String(); len(s) != tailSize || !strings.HasSuffix(s, "the reason") {
		t.Fatalf("tail = %d bytes ending %q", len(s), s[len(s)-10:])
	}
}
