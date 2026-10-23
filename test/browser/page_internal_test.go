package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/test/trace"
)

func fakePage(c *captured, f *fakeDriver, opts ...Option) *Page {
	return New(c, f, append([]Option{BaseURL("http://app.test")}, opts...)...)
}

func TestTheStepFailsOnWhatThePageReportsByItself(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		want  string
		kind  string
	}{
		{"console.error", Event{Kind: EventConsole, Level: "error", Source: "console-api", Text: "kaput"}, "console.error: kaput", "console-error"},
		{"exception", Event{Kind: EventException, Text: "TypeError: x is undefined"}, "uncaught exception: TypeError: x is undefined", "exception"},
		{"5xx", Event{Kind: EventResponse, RequestID: "7", Method: "POST", URL: "http://app.test/pay", Status: 502}, "POST http://app.test/pay answered 502", "server-error"},
		{"CSP", Event{Kind: EventConsole, Level: "error", Source: "console-api", Text: cspPrefix + "inline (script-src-elem)"}, "Content-Security-Policy blocked inline (script-src-elem)", "csp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
			f := &fakeDriver{}
			f.onNav = func(string) { f.emit(tc.event) }
			c := run(t, func(c *captured) {
				fakePage(c, f).Visit("/")
				c.Logf("reached past the failing step")
			})
			if !strings.Contains(c.text(), "the page failed: "+tc.want) {
				t.Fatalf("failure = %q, want it to name %q", c.text(), tc.want)
			}
			for _, line := range c.logs {
				if strings.Contains(line, "reached past") {
					t.Fatal("the test went on after the page failed")
				}
			}
			got, err := trace.Read(trace.Dir(c.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Failures) != 1 || got.Failures[0].Kind != tc.kind {
				t.Fatalf("trace failures = %+v", got.Failures)
			}
		})
	}
}

func TestWhatIsNotAFailureDoesNotFail(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	f := &fakeDriver{}
	f.onNav = func(string) {
		f.emit(Event{Kind: EventConsole, Level: "error", Source: "network", Text: "Failed to load resource: 404"})
		f.emit(Event{Kind: EventConsole, Level: "warning", Source: "console-api", Text: "deprecated"})
		f.emit(Event{Kind: EventRequest, RequestID: "1", URL: "http://app.test/missing", Method: "GET"})
		f.emit(Event{Kind: EventResponse, RequestID: "1", URL: "http://app.test/missing", Status: 404})
		f.emit(Event{Kind: EventRequestFinished, RequestID: "1"})
		f.emit(Event{Kind: EventRequestFailed, RequestID: "2", Text: "net::ERR_ABORTED"})
	}
	c := run(t, func(c *captured) { fakePage(c, f).Visit("/") })
	if c.Failed() {
		t.Fatalf("the page failed on a 404, a warning or an aborted request: %s", c.text())
	}
}

func TestAllowAcceptsTheFailureATestExpects(t *testing.T) {
	f := &fakeDriver{}
	f.onNav = func(string) {
		f.emit(Event{Kind: EventResponse, RequestID: "1", URL: "http://app.test/boom", Status: 500})
		f.emit(Event{Kind: EventConsole, Level: "error", Source: "console-api", Text: "unexpected"})
	}
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	c := run(t, func(c *captured) {
		fakePage(c, f).Allow("answered 500").Visit("/")
	})
	if !strings.Contains(c.text(), "console.error: unexpected") || strings.Contains(c.text(), "answered 500") {
		t.Fatalf("failure = %q", c.text())
	}
}

func TestAFailureAfterTheLastStepFailsAtTheEnd(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	f := &fakeDriver{}
	c := run(t, func(c *captured) {
		fakePage(c, f).Visit("/")
		f.emit(Event{Kind: EventException, Text: "late"})
	})
	if !strings.Contains(c.text(), "after the last step: uncaught exception: late") {
		t.Fatalf("failure = %q", c.text())
	}
}

func TestSettleWaitsForTheNetwork(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	f := &fakeDriver{}
	f.onNav = func(string) {
		f.emit(Event{Kind: EventRequest, RequestID: "slow", URL: "http://app.test/api"})
		f.emit(Event{Kind: EventRequest, RequestID: "stream", URL: "http://app.test/events", ResourceType: "EventSource"})
	}
	c := run(t, func(c *captured) { fakePage(c, f, Timeout(300*time.Millisecond)).Visit("/") })
	if !strings.Contains(c.text(), "the page did not settle: 1 request(s) in flight") {
		t.Fatalf("a request in flight did not hold the step: %q", c.text())
	}

	finished := make(chan struct{})
	f.onNav = func(string) {
		f.emit(Event{Kind: EventRequest, RequestID: "slow", URL: "http://app.test/api"})
		go func() {
			timer := time.NewTimer(150 * time.Millisecond)
			<-timer.C
			f.emit(Event{Kind: EventRequestFinished, RequestID: "slow"})
			close(finished)
		}()
	}
	var took time.Duration
	c = run(t, func(c *captured) {
		p := fakePage(c, f, Timeout(2*time.Second))
		start := time.Now()
		p.Visit("/")
		took = time.Since(start)
	})
	if c.Failed() {
		t.Fatal(c.text())
	}
	select {
	case <-finished:
	default:
		t.Fatal("Visit returned before the request finished")
	}
	if took < 150*time.Millisecond+defaultQuiet {
		t.Fatalf("Visit returned %s after it started, before the network was quiet for %s", took, defaultQuiet)
	}

	f.onNav = func(string) { f.emit(Event{Kind: EventNavigationStarted}) }
	c = run(t, func(c *captured) { fakePage(c, f, Timeout(200*time.Millisecond)).Visit("/") })
	if !strings.Contains(c.text(), "a navigation is loading") {
		t.Fatalf("a navigation in progress did not hold the step: %q", c.text())
	}
}

func TestVisitResolvesPathsURLsAndRouteNames(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	f := &fakeDriver{}
	routes := func(name string, params ...any) (string, error) {
		if name == "invoices.show" {
			return fmt.Sprintf("/invoices/%v", params[0]), nil
		}
		return "", errors.New("no such route")
	}
	c := run(t, func(c *captured) {
		p := fakePage(c, f, Routes(routes))
		p.Visit("/dashboard")
		p.Visit("invoices.show", 42)
		p.Visit("https://elsewhere.test/x")
	})
	if c.Failed() {
		t.Fatal(c.text())
	}
	want := []string{"http://app.test/dashboard", "http://app.test/invoices/42", "https://elsewhere.test/x"}
	if strings.Join(f.navigated, " ") != strings.Join(want, " ") {
		t.Fatalf("navigated = %v, want %v", f.navigated, want)
	}

	for _, tc := range []struct {
		name string
		opts []Option
		at   string
		want string
	}{
		{"unknown route", []Option{Routes(routes)}, "nope", `route "nope": no such route`},
		{"no resolver", nil, "invoices.index", "was given no Routes resolver"},
		{"no base", []Option{BaseURL("")}, "/x", "has no BaseURL"},
	} {
		c := run(t, func(c *captured) { fakePage(c, &fakeDriver{}, tc.opts...).Visit(tc.at) })
		if !strings.Contains(c.text(), tc.want) {
			t.Fatalf("%s: failure = %q, want %q", tc.name, c.text(), tc.want)
		}
	}
}

func TestTheTraceIsWrittenOnFailureAndOnRequest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARANDU_E2E_TRACE_DIR", dir)
	t.Setenv("ARANDU_E2E_TRACE", "")

	f := &fakeDriver{text: "Invoices"}
	f.onNav = func(string) {
		f.emit(Event{Kind: EventRequest, RequestID: "1", Method: "GET", URL: "http://app.test/"})
		f.emit(Event{Kind: EventResponse, RequestID: "1", URL: "http://app.test/", Status: 200, Header: http.Header{"X-Request-Id": {"req-1"}}})
		f.emit(Event{Kind: EventRequestFinished, RequestID: "1"})
		f.emit(Event{Kind: EventConsole, Level: "log", Source: "console-api", Text: "hello"})
	}
	passing := run(t, func(c *captured) {
		c.name = "TestPasses"
		p := fakePage(c, f)
		p.Visit("/")
		p.ExpectText("Invoices")
	})
	if passing.Failed() {
		t.Fatal(passing.text())
	}
	if _, err := os.Stat(filepath.Join(dir, "TestPasses")); !os.IsNotExist(err) {
		t.Fatalf("a passing test wrote a trace without ARANDU_E2E_TRACE: %v", err)
	}

	failing := run(t, func(c *captured) {
		c.name = "TestFails"
		p := fakePage(c, f, Timeout(500*time.Millisecond))
		p.Visit("/")
		p.ExpectText("Receipts")
	})
	if !strings.Contains(failing.text(), `"Receipts" is not on the page, which reads: "Invoices"`) {
		t.Fatalf("failure = %q", failing.text())
	}
	got, err := trace.Read(filepath.Join(dir, "TestFails"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Failed || got.Driver != "fake" || len(got.Steps) != 3 {
		t.Fatalf("trace = failed %t, driver %q, %d steps", got.Failed, got.Driver, len(got.Steps))
	}
	if got.Steps[1].Action != "expect text" || !strings.Contains(got.Steps[1].Error, "Receipts") || got.Steps[1].Screenshot == "" || got.Steps[1].DOM == "" {
		t.Fatalf("the failing step = %+v", got.Steps[1])
	}
	if got.Steps[2].Action != "end" {
		t.Fatalf("the last step is %q, not the end capture", got.Steps[2].Action)
	}
	if len(got.Network) != 1 || got.Network[0].ServerRequestID != "req-1" || got.Network[0].Status != 200 || got.Network[0].Finished.IsZero() {
		t.Fatalf("network = %+v", got.Network)
	}
	if len(got.Console) != 1 || got.Console[0].Text != "hello" {
		t.Fatalf("console = %+v", got.Console)
	}
	if !strings.Contains(strings.Join(failing.logs, "\n"), filepath.Join(dir, "TestFails")) {
		t.Fatalf("the failure did not say where the trace is: %v", failing.logs)
	}

	t.Setenv("ARANDU_E2E_TRACE", "1")
	run(t, func(c *captured) {
		c.name = "TestPassesTraced"
		fakePage(c, f).Visit("/")
	})
	if got, err := trace.Read(filepath.Join(dir, "TestPassesTraced")); err != nil || got.Failed {
		t.Fatalf("ARANDU_E2E_TRACE=1 did not write the passing test's trace: %v", err)
	}
}

func TestTheServerSideLandsInTheSameTrace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARANDU_E2E_TRACE_DIR", dir)
	rec := trace.NewRecorder("TestShared")
	c := run(t, func(c *captured) {
		c.name = "TestShared"
		p := fakePage(c, &fakeDriver{}, WithTrace(rec))
		p.Trace().Server("req-1", trace.Server{Decisions: []trace.Decision{{Policy: "InvoicePolicy", Action: "view", Allowed: false}}})
		p.Visit("/")
		c.Errorf("fail on purpose")
	})
	if c.text() != "fail on purpose" {
		t.Fatal(c.text())
	}
	got, err := trace.Read(filepath.Join(dir, "TestShared"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Server["req-1"] == nil || got.Server["req-1"].Decisions[0].Policy != "InvoicePolicy" {
		t.Fatalf("server = %+v", got.Server)
	}
}

func TestLocators(t *testing.T) {
	l := Role("button", "Delete").In(Modal("Delete invoice"))
	if l.String() != `modal name="Delete invoice" >> role=button name="Delete"` {
		t.Fatalf("String = %s", l)
	}
	encoded, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"kind":"role","a":"button","b":"Delete","parent":{"kind":"modal","a":"Delete invoice"}}` {
		t.Fatalf("JSON = %s", encoded)
	}
	if p := CSS(".btn", " ").problem(); !strings.Contains(p, "has no reason") {
		t.Fatalf("a CSS locator with a blank reason passed: %q", p)
	}
	if p := Text("x").In(CSS(".card", "")).problem(); !strings.Contains(p, "has no reason") {
		t.Fatalf("a parent CSS locator with no reason passed: %q", p)
	}
	if p := (Locator{}).problem(); !strings.Contains(p, "zero Locator") {
		t.Fatalf("the zero locator passed: %q", p)
	}
	if p := CSS("#log", "fixture without a role").problem(); p != "" {
		t.Fatalf("a justified CSS locator was refused: %q", p)
	}
	if TestID("total").String() != `testid="total"` || Label("Email").String() != `label="Email"` || Text("Save").String() != `text="Save"` {
		t.Fatal("String of the simple locators")
	}
}

func TestACSSLocatorWithoutAReasonFailsTheStep(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	c := run(t, func(c *captured) { fakePage(c, &fakeDriver{}).Click(CSS(".btn-primary", "")) })
	if !strings.Contains(c.text(), "has no reason") {
		t.Fatalf("failure = %q", c.text())
	}
}

func TestPauseDoesNothingWithoutAWindow(t *testing.T) {
	t.Setenv("ARANDU_E2E_HEADED", "")
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	c := run(t, func(c *captured) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { fakePage(c, &fakeDriver{}).Pause(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			c.Errorf("Pause blocked with no window to look at")
		}
	})
	if c.Failed() {
		t.Fatal(c.text())
	}
}
