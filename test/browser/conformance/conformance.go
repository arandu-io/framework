// Package conformance is the suite every browser driver passes. A driver
// calls it from one test:
//
//	func TestConformance(t *testing.T) {
//		conformance.Run(t, cdp.Launch)
//	}
//
// It serves a small site from the test and drives it through browser.Page,
// the way an application's tests do, so what it proves is what those tests
// rely on: the page waits for htmx to settle, for the network to go idle and
// for animations to end; it fails by itself on an exception, a console.error,
// a 5xx and a Content-Security-Policy violation, and writes the trace when it
// does; a modal is found by its name; the viewport, the colour scheme, the
// screenshot, the pointer and the keyboard work; and two drivers share no
// cookie or storage.
//
// A check that proves a failure runs the page under a stand-in for testing.T
// that records the failure instead of failing the suite, and then reads the
// trace the failure wrote.
package conformance

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/framework/test/trace"
)

// Run runs the whole suite against drivers launched by factory, with the
// options the environment sets.
func Run(t *testing.T, factory browser.Factory) {
	t.Helper()
	opts, err := browser.OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(site())
	t.Cleanup(srv.Close)
	s := &suite{factory: factory, opts: opts, base: srv.URL}

	t.Run("navigation and events", s.navigation)
	t.Run("htmx settles before an action returns", s.htmx)
	t.Run("network idle and animations", s.idle)
	t.Run("form input", s.form)
	t.Run("keyboard", s.keyboard)
	t.Run("locators are strict", s.strict)
	t.Run("a covered control is not clicked", s.covered)
	t.Run("modal", s.modal)
	t.Run("console error fails the test", s.failsOn("/console", "console-error", "console.error: conformance console error 42"))
	t.Run("uncaught exception fails the test", s.failsOn("/exception", "exception", "conformance exception"))
	t.Run("5xx fails the test", s.failsOn("/server-error", "server-error", "answered 503"))
	t.Run("CSP violation fails the test", s.failsOn("/csp", "csp", "Content-Security-Policy blocked inline (script-src-elem)"))
	t.Run("allowed failures pass", s.allowed)
	t.Run("viewport and media", s.viewport)
	t.Run("responsive overflow", s.responsive)
	t.Run("capture", s.capture)
	t.Run("isolation", s.isolation)
}

type suite struct {
	factory browser.Factory
	opts    browser.Options
	base    string
}

func (s *suite) open(t *testing.T, opts ...browser.Option) *browser.Page {
	t.Helper()
	d := browser.Launch(t, s.factory, s.opts)
	return browser.New(t, d, append([]browser.Option{browser.BaseURL(s.base)}, opts...)...)
}

func (s *suite) navigation(t *testing.T) {
	p := s.open(t)
	p.Visit("/")
	p.ExpectText("The suite every driver passes.")
	p.ExpectURL("/")
	p.ExpectVisible(browser.Role("heading", "Conformance"))

	snapshot := p.Trace().Snapshot()
	var document *trace.Request
	for i, q := range snapshot.Network {
		if q.URL == s.base+"/" {
			document = &snapshot.Network[i]
		}
	}
	if document == nil || document.Status != 200 || document.Method != "GET" || document.Finished.IsZero() {
		t.Fatalf("the driver did not report the document's request, response and end: %+v", snapshot.Network)
	}
	if document.ServerRequestID != "conformance-" {
		t.Fatalf("the response headers did not reach the trace: server request id %q", document.ServerRequestID)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := p.Driver().Navigate(ctx, "http://127.0.0.1:1/")
	if err == nil || !strings.Contains(err.Error(), "ERR_") {
		t.Fatalf("a refused navigation must carry the browser's reason, got %v", err)
	}
	err = p.Driver().Evaluate(ctx, "(() => { throw new Error('kaput') })()", nil)
	if err == nil || !strings.Contains(err.Error(), "kaput") {
		t.Fatalf("an exception in an evaluation must carry its message, got %v", err)
	}
	var sum int
	if err := p.Driver().Evaluate(ctx, "Promise.resolve(40 + 2)", &sum); err != nil || sum != 42 {
		t.Fatalf("an evaluated promise must be awaited: %d, %v", sum, err)
	}
}

func (s *suite) htmx(t *testing.T) {
	p := s.open(t)
	p.Visit("/htmx")

	p.ClickRole("button", "Load")
	var state struct {
		Text     string `json:"text"`
		Settling bool   `json:"settling"`
	}
	p.Evaluate(`({text: document.getElementById('out').textContent, settling: !!document.querySelector('.htmx-settling')})`, &state)
	if state.Text != "Loaded" || state.Settling {
		t.Fatalf("the click returned before htmx swapped and settled: %+v", state)
	}

	p.ClickRole("button", "Replace me")
	var replaced bool
	p.Evaluate(`!!document.getElementById('replaced')`, &replaced)
	if !replaced {
		t.Fatal("the click returned before an element replacing itself was swapped")
	}

	p.ClickRole("button", "Ping")
	var quiet bool
	p.Evaluate(`!document.querySelector('.htmx-request')`, &quiet)
	if !quiet {
		t.Fatal("the click returned while a request with nothing to swap was in flight")
	}
}

func (s *suite) idle(t *testing.T) {
	p := s.open(t)
	p.Visit("/late")
	var text string
	p.Evaluate(`document.getElementById('api').textContent`, &text)
	if text != "arrived" {
		t.Fatalf("Visit returned before the page's own request finished: %q", text)
	}

	p.Visit("/animated")
	var running int
	p.Evaluate(`document.getAnimations().filter(a => a.playState === 'running').length`, &running)
	if running != 0 {
		t.Fatalf("Visit returned with %d animation(s) running", running)
	}
}

func (s *suite) form(t *testing.T) {
	p := s.open(t)
	p.Visit("/form")
	p.Fill("Email", "ana@example.test")
	p.Select("Plan", "Pro")
	p.Check("I agree")
	p.Uncheck("Newsletter")
	p.FillIn(browser.Label("Note"), "first line")
	p.ClickRole("button", "Send")
	p.ExpectURL("/done?agree=on&email=ana%40example.test&news=&note=first+line&plan=pro")
	p.ExpectText("email=ana@example.test plan=pro agree=on news= note=first line")

	p.Visit("/form")
	p.Fill("Email", "bia@example.test")
	p.Submit()
	p.ExpectText("email=bia@example.test plan=basic agree= news=on")

	p.Visit("/form")
	p.Fill("Email", "")
	p.Fill("Email", "caio@example.test")
	p.Press("Enter")
	p.ExpectText("email=caio@example.test")
}

func (s *suite) keyboard(t *testing.T) {
	p := s.open(t)
	p.Visit("/keys")
	p.Fill("First", "x")
	p.Press("Tab")
	var focused string
	p.Evaluate(`document.activeElement.id`, &focused)
	if focused != "b" {
		t.Fatalf("Tab moved the focus to %q, not to the second field", focused)
	}
	p.Press("Shift+Tab")
	p.Press("a")
	p.Press("Escape")
	p.Find(browser.CSS("#log", "the log is a test fixture with no role")).ExpectText("Tab Shift+Tab a Escape")
	var value string
	p.Evaluate(`document.getElementById('a').value`, &value)
	// Focus arriving by keyboard selects the field's text, so the key replaces it.
	if value != "a" {
		t.Fatalf("the typed key did not replace the text the keyboard focus selected: %q", value)
	}
}

func (s *suite) strict(t *testing.T) {
	p := s.open(t)
	p.Visit("/twice")
	p.Click(browser.TestID("only"))
	var ambiguous time.Duration
	msg, _ := s.expectFailure(t, func(p *browser.Page) {
		p.Visit("/twice")
		start := time.Now()
		defer func() { ambiguous = time.Since(start) }()
		p.ClickRole("button", "Save")
	}, browser.Timeout(5*time.Second))
	if !strings.Contains(msg, `3 visible elements match role=button name="Save"`) {
		t.Fatalf("an ambiguous locator must fail naming the matches, got: %s", msg)
	}
	// Waiting cannot make two matches one, so the failure does not wait.
	if ambiguous > 2*time.Second {
		t.Fatalf("an ambiguous locator failed after %s, waiting for the timeout instead of at once", ambiguous)
	}
	msg, _ = s.expectFailure(t, func(p *browser.Page) {
		p.Visit("/twice")
		p.Click(browser.CSS("button", ""))
	})
	if !strings.Contains(msg, "has no reason") {
		t.Fatalf("a CSS locator without a reason must fail, got: %s", msg)
	}
}

func (s *suite) covered(t *testing.T) {
	msg, _ := s.expectFailure(t, func(p *browser.Page) {
		p.Visit("/covered")
		p.ClickRole("button", "Hidden behind")
	}, browser.Timeout(time.Second))
	if !strings.Contains(msg, "is covered by div#overlay") {
		t.Fatalf("a covered control must fail naming what covers it, got: %s", msg)
	}
}

func (s *suite) modal(t *testing.T) {
	p := s.open(t)
	p.Visit("/modal")
	p.ClickRole("button", "Delete")
	p.ExpectModal("Delete invoice").ClickRole("button", "Delete")
	p.ExpectHidden(browser.Modal(""))
	p.ExpectText("Deleted")
}

// failsOn proves the page fails the test by itself on visiting path, records
// the failure as kind, and writes a complete trace.
func (s *suite) failsOn(path, kind, text string) func(t *testing.T) {
	return func(t *testing.T) {
		msg, dir := s.expectFailure(t, func(p *browser.Page) {
			p.Visit(path)
			p.ExpectText("never reached")
		})
		if !strings.Contains(msg, "the page failed") || !strings.Contains(msg, text) {
			t.Fatalf("visiting %s must fail with %q, got: %s", path, text, msg)
		}
		got, err := trace.Read(dir)
		if err != nil {
			t.Fatalf("the failure wrote no trace: %v", err)
		}
		defer os.RemoveAll(dir)
		if !got.Failed {
			t.Fatal("the trace does not say the test failed")
		}
		var failure bool
		for _, f := range got.Failures {
			failure = failure || (f.Kind == kind && strings.Contains(f.Text, text))
		}
		if !failure {
			t.Fatalf("the trace has no %s failure: %+v", kind, got.Failures)
		}
		if len(got.Network) == 0 {
			t.Fatal("the trace has no network")
		}
		if len(got.Steps) < 2 {
			t.Fatalf("the trace has %d steps", len(got.Steps))
		}
		for _, step := range got.Steps {
			if step.Screenshot == "" || step.DOM == "" {
				t.Fatalf("step %d (%s) has no screenshot or DOM: %+v", step.Index, step.Action, step)
			}
			shot, err := os.ReadFile(filepath.Join(dir, step.Screenshot))
			if err != nil || !bytes.HasPrefix(shot, []byte("\x89PNG")) {
				t.Fatalf("step %d screenshot: %v", step.Index, err)
			}
			if _, err := os.Stat(filepath.Join(dir, step.DOM)); err != nil {
				t.Fatalf("step %d DOM: %v", step.Index, err)
			}
		}
		if kind == "console-error" && len(got.Console) == 0 {
			t.Fatal("the trace has no console")
		}
	}
}

func (s *suite) allowed(t *testing.T) {
	p := s.open(t)
	p.Allow("answered 503")
	p.Visit("/server-error")
	p.ExpectText("server error")
}

func (s *suite) viewport(t *testing.T) {
	p := s.open(t)
	p.Visit("/media")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := p.Driver().SetViewport(ctx, browser.Viewport{Width: 390, Height: 844}); err != nil {
		t.Fatal(err)
	}
	if err := p.Driver().EmulateMedia(ctx, browser.Media{ColorScheme: "dark", ReducedMotion: "reduce"}); err != nil {
		t.Fatal(err)
	}
	var seen struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Dark   bool   `json:"dark"`
		Motion bool   `json:"motion"`
		Body   string `json:"body"`
	}
	p.Evaluate(`({width: innerWidth, height: innerHeight, dark: matchMedia('(prefers-color-scheme: dark)').matches, motion: matchMedia('(prefers-reduced-motion: reduce)').matches, body: getComputedStyle(document.body).backgroundColor})`, &seen)
	if seen.Width != 390 || seen.Height != 844 || !seen.Dark || !seen.Motion || seen.Body != "rgb(0, 0, 0)" {
		t.Fatalf("the page sees %+v", seen)
	}
}

func (s *suite) responsive(t *testing.T) {
	p := s.open(t)
	p.Visit("/narrow")
	p.Responsive()
	var width int
	p.Evaluate(`innerWidth`, &width)
	if width != 1440 {
		t.Fatalf("Responsive left the viewport at %d", width)
	}

	msg, dir := s.expectFailure(t, func(p *browser.Page) {
		p.Visit("/wide")
		p.Responsive()
	})
	defer os.RemoveAll(dir)
	if !strings.Contains(msg, "wider than the viewport at 768 light, 768 dark, 390 light, 390 dark, 320 light, 320 dark") {
		t.Fatalf("Responsive must name every combination that overflows, got: %s", msg)
	}
	got, err := trace.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	geometry := 0
	for _, step := range got.Steps {
		if step.Action == "responsive" && strings.HasPrefix(step.Target, "320") {
			if !strings.Contains(string(step.Data), `div#wide`) || step.Screenshot == "" {
				t.Fatalf("the overflowing step carries no geometry naming the element: %s", step.Data)
			}
			geometry++
		}
		if step.Action == "responsive" && step.Target == "1440 light" && len(step.Data) != 0 {
			t.Fatalf("a combination that fits carries geometry: %s", step.Data)
		}
	}
	if geometry != 2 {
		t.Fatalf("%d steps at 320 carry geometry, want 2", geometry)
	}
}

func (s *suite) capture(t *testing.T) {
	p := s.open(t, browser.WithViewport(browser.Viewport{Width: 800, Height: 600}))
	p.Visit("/")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	shot, err := p.Driver().Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(shot))
	if err != nil {
		t.Fatalf("the screenshot is not a PNG: %v", err)
	}
	if cfg.Width != 800 || cfg.Height != 600 {
		t.Fatalf("the screenshot is %dx%d, the viewport 800x600", cfg.Width, cfg.Height)
	}
}

func (s *suite) isolation(t *testing.T) {
	first, second := s.open(t), s.open(t)
	first.Visit("/cookie/set?v=first")
	first.Evaluate(`localStorage.setItem('conformance', 'first')`, nil)
	first.Visit("/cookie/echo")
	first.ExpectText("cookie=first")

	second.Visit("/cookie/echo")
	second.ExpectText("cookie=none")
	var stored *string
	second.Evaluate(`localStorage.getItem('conformance')`, &stored)
	if stored != nil {
		t.Fatalf("the second driver sees the first one's storage: %q", *stored)
	}

	second.SetCookies(&http.Cookie{Name: "conformance", Value: "planted", HttpOnly: true})
	second.Visit("/cookie/echo")
	second.ExpectText("cookie=planted")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cookies, err := second.Driver().Cookies(ctx, s.base)
	if err != nil || len(cookies) != 1 || cookies[0].Value != "planted" || !cookies[0].HttpOnly {
		t.Fatalf("cookies = %+v, %v", cookies, err)
	}
	cookies, err = first.Driver().Cookies(ctx, s.base)
	if err != nil || len(cookies) != 1 || cookies[0].Value != "first" {
		t.Fatalf("the first driver's cookies changed: %+v, %v", cookies, err)
	}
}

// expectFailure runs scenario on a page whose testing.T records instead of
// failing, runs the page's cleanup as the end of a test does, and returns
// what the page reported and the directory its trace went to.
func (s *suite) expectFailure(t *testing.T, scenario func(p *browser.Page), opts ...browser.Option) (string, string) {
	t.Helper()
	d := browser.Launch(t, s.factory, s.opts)
	c := &captured{TB: t, name: t.Name() + "/expected failure"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scenario(browser.New(c, d, append([]browser.Option{browser.BaseURL(s.base)}, opts...)...))
	}()
	<-done
	c.runCleanups()
	if !c.Failed() {
		t.Fatal("the scenario was expected to fail the test and did not")
	}
	return c.messages(), trace.Dir(c.name)
}

// captured is a testing.TB that records failures. Its Fatal and Skip end the
// goroutine they run on, as testing.T's do, so it is used on a goroutine of
// its own.
type captured struct {
	testing.TB
	name string

	mu       sync.Mutex
	failed   bool
	lines    []string
	cleanups []func()
}

func (c *captured) record(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed = true
	c.lines = append(c.lines, line)
}

func (c *captured) messages() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func (c *captured) runCleanups() {
	c.mu.Lock()
	list := c.cleanups
	c.cleanups = nil
	c.mu.Unlock()
	for i := len(list) - 1; i >= 0; i-- {
		list[i]()
	}
}

func (c *captured) Name() string                 { return c.name }
func (c *captured) Helper()                      {}
func (c *captured) Cleanup(f func())             { c.mu.Lock(); c.cleanups = append(c.cleanups, f); c.mu.Unlock() }
func (c *captured) Error(args ...any)            { c.record(fmt.Sprint(args...)) }
func (c *captured) Errorf(f string, args ...any) { c.record(fmt.Sprintf(f, args...)) }
func (c *captured) Fail()                        { c.record("Fail") }
func (c *captured) FailNow()                     { c.record("FailNow"); runtime.Goexit() }
func (c *captured) Fatal(args ...any)            { c.record(fmt.Sprint(args...)); runtime.Goexit() }
func (c *captured) Fatalf(f string, args ...any) { c.record(fmt.Sprintf(f, args...)); runtime.Goexit() }
func (c *captured) Skip(args ...any)             { c.record("skip: " + fmt.Sprint(args...)); runtime.Goexit() }
func (c *captured) Skipf(f string, args ...any) {
	c.record("skip: " + fmt.Sprintf(f, args...))
	runtime.Goexit()
}
func (c *captured) SkipNow()                   { c.record("SkipNow"); runtime.Goexit() }
func (c *captured) Failed() bool               { c.mu.Lock(); defer c.mu.Unlock(); return c.failed }
func (c *captured) Log(args ...any)            { c.TB.Log(args...) }
func (c *captured) Logf(f string, args ...any) { c.TB.Logf(f, args...) }
