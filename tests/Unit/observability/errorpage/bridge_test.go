// Tests of the bridge, and of nothing else.
//
// What the page CONTAINS is tested in github.com/arandu-io/hesape/exception,
// against the template that now draws it; the tests that used to live here were
// tests of an implementation this package no longer holds.
//
// What is left to prove is that the old names reach the new behaviour, and here
// that is more than a compile-time assertion: both entry points are envelopes
// that build a Handler and drive it through a door that was not built for them,
// so each one gets a round trip that fails if a field is dropped on the way.

package unit

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/observability/errorpage"
	"github.com/arandu-io/hesape/exception"
	hlog "github.com/arandu-io/hesape/log"
)

const (
	appModule       = "github.com/example/app"
	frameworkPrefix = "github.com/arandu-io/framework"
	hesapePrefix    = "github.com/arandu-io/hesape"
)

// TestStackFrameIsTheHesapeType is the alias half of this bridge, which is one
// type wide.
func TestStackFrameIsTheHesapeType(t *testing.T) {
	var (
		_ exception.StackFrame = errorpage.StackFrame{}
		_ errorpage.StackFrame = exception.StackFrame{}
	)
}

// TestCaptureCollapsesHesapeFrames is the reason Capture is a call through and
// not the code it used to be.
//
// The rule here was "collapse github.com/arandu-io/framework", written when the
// implementation was in that module. After the move it matched nothing that
// runs: a hesape frame fell through every branch, was called application code,
// and the page expanded it and read its source off disk. hesape asks about its
// own import path, so the frames are collapsed again -- and the frames of this
// bridge are collapsed with them, through the fallback that trusts the module
// the application declared.
func TestCaptureCollapsesHesapeFrames(t *testing.T) {
	frames := captureBelowHesape(t, appModule)

	var sawHesape, sawFramework bool
	for _, f := range frames {
		switch {
		case strings.HasPrefix(f.Func, hesapePrefix):
			sawHesape = true
			if f.IsApp {
				t.Errorf("hesape frame %s is expanded as application code", f.Func)
			}
		case strings.HasPrefix(f.Func, frameworkPrefix):
			sawFramework = true
			if f.IsApp {
				t.Errorf("framework frame %s is expanded as application code", f.Func)
			}
		}
	}
	if !sawHesape || !sawFramework {
		t.Fatalf("the stack did not carry both kinds of frame: hesape %v, framework %v", sawHesape, sawFramework)
	}
}

// TestCaptureWithoutAppModuleIsGenerous writes down what the fix leaves behind.
//
// With no module path there is nothing left to tell the application from what
// it imported, and hesape is generous on purpose: it expands everything that is
// not its own code and not the standard library, the frames of this bridge
// included. The old rule collapsed them even here. It is the case Options
// exists to avoid, and both skeletons set the field.
func TestCaptureWithoutAppModuleIsGenerous(t *testing.T) {
	frames := captureBelowHesape(t, "")

	for _, f := range frames {
		if strings.HasPrefix(f.Func, hesapePrefix) && f.IsApp {
			t.Errorf("hesape frame %s is expanded even with no application module", f.Func)
		}
	}
}

// captureBelowHesape takes a stack with a hesape frame on it, which is the one
// thing a capture from a plain test function cannot produce.
func captureBelowHesape(t *testing.T, module string) []errorpage.StackFrame {
	t.Helper()

	var frames []errorpage.StackFrame
	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		frames = errorpage.Capture(1, module)
	})
	hlog.Middleware(slog.New(slog.DiscardHandler))(inner).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if len(frames) == 0 {
		t.Fatal("Capture answered no frames")
	}
	return frames
}

// TestRenderDrawsTheDebugPage is the round trip for the first envelope.
//
// Every assertion is a field of Options that the translation to
// exception.Config could drop without failing to compile: the panic value, the
// Collector that arrives as an argument rather than on the context, the editor
// and the diagnosis callback.
func TestRenderDrawsTheDebugPage(t *testing.T) {
	col := observability.NewCollector("req-1")
	observability.Dump(observability.WithCollector(context.Background(), col), "widget", "42")

	w := httptest.NewRecorder()
	errorpage.Render(
		w,
		httptest.NewRequest(http.MethodGet, "/orders/9", nil),
		"boom",
		col,
		errorpage.Options{
			Editor:    "zed",
			AppModule: appModule,
			Diagnose:  func(context.Context) []string { return []string{"the outbox is stuck"} },
		},
	)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status is %d, want 500 for a panic nothing classifies", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"boom", "widget", "req-1", "the outbox is stuck", "zed://file"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not carry %q", want)
		}
	}
}

// TestRenderCarriesAPanickedError covers the half of the panic value that does
// survive the wrapper: a value that is not an error reaches the page as its
// text, and only the Go type on the subtitle is the wrapper's.
func TestRenderCarriesAPanickedError(t *testing.T) {
	w := httptest.NewRecorder()
	errorpage.Render(
		w,
		httptest.NewRequest(http.MethodGet, "/", nil),
		struct{ Reason string }{Reason: "no rows"},
		nil,
		errorpage.Options{AppModule: appModule},
	)

	if !strings.Contains(w.Body.String(), "no rows") {
		t.Error("the page does not carry the text of a panic value that was not an error")
	}
}

// TestRenderDumpDrawsTheDumpPage is the round trip for the second envelope, and
// it is the one that would fail silently.
//
// The dump page is drawn by an unexported method whose one caller is Recover,
// so this envelope reaches it by raising the dump-and-die sentinel inside it. If
// hesape stops recognising the sentinel there, the caller gets a 500 error page
// where the dump should be, and nothing else says so.
func TestRenderDumpDrawsTheDumpPage(t *testing.T) {
	col := observability.NewCollector("req-2")
	observability.Dump(observability.WithCollector(context.Background(), col), "widget", "42")

	w := httptest.NewRecorder()
	errorpage.RenderDump(
		w,
		httptest.NewRequest(http.MethodGet, "/orders/9", nil),
		col,
		errorpage.Options{Editor: "zed", AppModule: appModule},
	)

	if w.Code != http.StatusOK {
		t.Errorf("status is %d, want 200: the request was aborted on purpose, not by a failure", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Dump", "widget", "req-2"} {
		if !strings.Contains(body, want) {
			t.Errorf("the dump page does not carry %q", want)
		}
	}
	if strings.Contains(body, "Stack") {
		t.Error("the dump page carries a stack, so the error page was drawn instead")
	}
}

// TestEditorLinkForwards keeps the one hop this package still makes: it asks
// observability, which is the bridge that asks hesape.
func TestEditorLinkForwards(t *testing.T) {
	want := observability.EditorLink("goland", "/src/app/main.go", 12)
	if got := errorpage.EditorLink("goland", "/src/app/main.go", 12); got != want {
		t.Errorf("EditorLink = %q, observability answers %q", got, want)
	}
}
