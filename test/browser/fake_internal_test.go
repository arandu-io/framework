package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeDriver is a Driver with no browser behind it. It answers the page
// script's calls with a settled page and lets a test raise events, which is
// enough to prove what Page does with them.
type fakeDriver struct {
	mu        sync.Mutex
	listener  func(Event)
	navigated []string
	onNav     func(url string)
	text      string
	closed    bool
	scripts   int
	failNav   error
}

func (f *fakeDriver) Info() Info { return Info{Driver: "fake", Browser: "none"} }

func (f *fakeDriver) Navigate(ctx context.Context, url string) error {
	f.mu.Lock()
	f.navigated = append(f.navigated, url)
	onNav, failNav := f.onNav, f.failNav
	f.mu.Unlock()
	if failNav != nil {
		return failNav
	}
	if onNav != nil {
		onNav(url)
	}
	return nil
}

func (f *fakeDriver) Evaluate(ctx context.Context, expression string, result any) error {
	var answer any
	switch {
	case expression == stateExpression:
		answer = map[string]any{"doc": "d", "ready": "complete", "htmx": true, "animating": false, "url": "http://app.test/", "leaving": -1}
	case strings.Contains(expression, "dom:"):
		answer = map[string]any{"url": "http://app.test/", "dom": "<html><body>fake</body></html>"}
	case strings.HasSuffix(expression, "text()"):
		f.mu.Lock()
		answer = f.text
		f.mu.Unlock()
	default:
		answer = nil
	}
	if result == nil {
		return nil
	}
	encoded, _ := json.Marshal(answer)
	return json.Unmarshal(encoded, result)
}

func (f *fakeDriver) AddInitScript(ctx context.Context, source string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts++
	return nil
}
func (f *fakeDriver) Click(ctx context.Context, x, y float64) error     { return nil }
func (f *fakeDriver) InsertText(ctx context.Context, text string) error { return nil }
func (f *fakeDriver) Press(ctx context.Context, key string) error       { return nil }
func (f *fakeDriver) SetViewport(ctx context.Context, v Viewport) error { return nil }
func (f *fakeDriver) EmulateMedia(ctx context.Context, m Media) error   { return nil }
func (f *fakeDriver) Screenshot(ctx context.Context) ([]byte, error) {
	return []byte("\x89PNG fake"), nil
}
func (f *fakeDriver) SetCookies(ctx context.Context, url string, cookies []*http.Cookie) error {
	return nil
}
func (f *fakeDriver) Cookies(ctx context.Context, url string) ([]*http.Cookie, error) {
	return nil, nil
}
func (f *fakeDriver) Listen(fn func(Event)) { f.mu.Lock(); f.listener = fn; f.mu.Unlock() }
func (f *fakeDriver) Close() error          { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }

// emit raises an event as the driver's goroutine would.
func (f *fakeDriver) emit(e Event) {
	f.mu.Lock()
	fn := f.listener
	f.mu.Unlock()
	fn(e)
}

// captured is a testing.TB that records failures instead of failing; Fatal
// and Skip end the goroutine, as testing.T's do.
type captured struct {
	testing.TB
	name string

	mu       sync.Mutex
	failed   bool
	skipped  bool
	lines    []string
	logs     []string
	cleanups []func()
}

func (c *captured) record(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed = true
	c.lines = append(c.lines, line)
}

func (c *captured) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func (c *captured) Name() string                 { return c.name }
func (c *captured) Helper()                      {}
func (c *captured) Cleanup(f func())             { c.mu.Lock(); c.cleanups = append(c.cleanups, f); c.mu.Unlock() }
func (c *captured) Errorf(f string, args ...any) { c.record(fmt.Sprintf(f, args...)) }
func (c *captured) Error(args ...any)            { c.record(fmt.Sprint(args...)) }
func (c *captured) Fatalf(f string, args ...any) { c.record(fmt.Sprintf(f, args...)); runtime.Goexit() }
func (c *captured) Fatal(args ...any)            { c.record(fmt.Sprint(args...)); runtime.Goexit() }
func (c *captured) FailNow()                     { c.record("FailNow"); runtime.Goexit() }
func (c *captured) Fail()                        { c.record("Fail") }
func (c *captured) Failed() bool                 { c.mu.Lock(); defer c.mu.Unlock(); return c.failed }
func (c *captured) Skipf(f string, args ...any) {
	c.mu.Lock()
	c.skipped = true
	c.lines = append(c.lines, "skip: "+fmt.Sprintf(f, args...))
	c.mu.Unlock()
	runtime.Goexit()
}
func (c *captured) Logf(f string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, fmt.Sprintf(f, args...))
}

// run runs body under a captured TB on a goroutine of its own, then the
// cleanups the way the end of a test does.
func run(t *testing.T, body func(c *captured)) *captured {
	t.Helper()
	c := &captured{TB: t, name: t.Name()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		body(c)
	}()
	<-done
	c.mu.Lock()
	list := c.cleanups
	c.mu.Unlock()
	for i := len(list) - 1; i >= 0; i-- {
		list[i]()
	}
	return c
}
