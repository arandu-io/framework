package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/httpx/middleware"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/observability/errorpage"
)

// pipeline builds the mandatory pipeline the way an application does, so these
// tests exercise the same composition a real request goes through.
func pipeline(dev bool, h http.Handler) http.Handler {
	return httpx.Chain(h,
		middleware.Recover(dev, errorpage.Options{Editor: "vscode"}),
		middleware.Observe(dev, "", nil),
		middleware.SecurityHeaders(dev),
	)
}

// TestPanicInDevelopmentShowsQueriesAndDumps is exit criterion 4 of phase 1: a
// deliberate panic in development shows the request's queries and dumps on the
// page. It is the whole product claim about debugging, so it is a test and not a
// manual step.
func TestPanicInDevelopmentShowsQueriesAndDumps(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		col := observability.FromContext(ctx)
		if col == nil {
			t.Fatal("in development the Collector must be installed in the context")
		}
		col.RecordQuery(`SELECT * FROM invoices WHERE tenant_id = $1`, []any{"t1"}, 2*time.Millisecond, 7, nil)
		observability.Dump(ctx, "invoice_total", 1999)
		panic("something the developer did not expect")
	})

	rec := httptest.NewRecorder()
	pipeline(true, handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/invoices", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"something the developer did not expect", // the panic
		"FROM invoices",                          // the query it ran first
		"invoice_total",                          // the dump label
		"1999",                                   // the dumped value
		"vscode://file",                          // a working open-in-editor link
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the debug page does not show %q", want)
		}
	}
	if id := rec.Header().Get("X-Request-ID"); id == "" || !strings.Contains(body, id) {
		t.Errorf("the page must carry the request id %q, to correlate with the log", id)
	}
}

// TestPanicInProductionLeaksNothing is the other half: the same panic outside
// development must not spill the stack, the SQL or the dumped values.
func TestPanicInProductionLeaksNothing(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if observability.FromContext(ctx) != nil {
			t.Fatal("without the tracing secret, production must carry no Collector")
		}
		observability.Dump(ctx, "customer_document", "secret-document-number")
		panic("connection to payments failed: token=secret-token")
	})

	rec := httptest.NewRecorder()
	pipeline(false, handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/checkout", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"secret-token", "secret-document-number", "goroutine", "middleware.", "panic"} {
		if strings.Contains(body, leak) {
			t.Errorf("the production response leaks %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "request_id:") {
		t.Error("the production response must carry the request id, or the log cannot be found")
	}
}

// TestTracingSecretEnablesTheCollector covers on-demand tracing in production.
func TestTracingSecretEnablesTheCollector(t *testing.T) {
	var enabled bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enabled = observability.FromContext(r.Context()) != nil
	})
	h := httpx.Chain(handler, middleware.Observe(false, "let-me-in", nil))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Arandu-Trace", "let-me-in")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !enabled {
		t.Error("the right secret must enable the Collector")
	}

	enabled = false
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Arandu-Trace", "wrong")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if enabled {
		t.Error("a wrong secret must not enable the Collector")
	}
}

func TestDumpDieRendersTheDumpPage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observability.DumpDie(r.Context(), "checkpoint", map[string]int{"step": 3})
		t.Error("execution continued past DumpDie")
	})

	rec := httptest.NewRecorder()
	pipeline(true, handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: dump-and-die is deliberate, not a failure", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "checkpoint") {
		t.Error("the dump page does not show the dump")
	}
}

// TestRequestIDIsSanitized: the inbound id lands in every log line of the
// request, so anything that is not a short hex string is an injection vector
// into the log aggregator.
func TestRequestIDIsSanitized(t *testing.T) {
	cases := map[string]bool{
		"a1b2c3d4":                        true,
		"5f2c-9a11":                       true,
		"trusted\nlevel=error msg=forged": false,
		"' OR 1=1":                        false,
		strings.Repeat("a", 65):           false,
	}

	for id, kept := range cases {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Request-ID", id)

		httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
			middleware.Observe(true, "", nil)).ServeHTTP(rec, r)

		got := rec.Header().Get("X-Request-ID")
		if kept && got != id {
			t.Errorf("id %q was rejected, want it kept", id)
		}
		if !kept && got == id {
			t.Errorf("id %q was echoed back, want a generated one", id)
		}
		if got == "" {
			t.Errorf("id %q produced no request id at all", id)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	pipeline(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	h := rec.Header()
	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Error("the CSP must never allow unsafe-inline: HTMX does not need it")
	}
}

// TestHSTSIsProductionOnly: sending HSTS over localhost pins http://localhost to
// https and breaks every developer's machine, in a way that survives a browser
// restart.
func TestHSTSIsProductionOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	pipeline(true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS in development = %q, want empty", got)
	}
}

// TestStatusWriterSupportsFlush protects server-sent events: HTMX streams over
// SSE, and losing Flush behind the wrapper breaks it in a way that is very hard
// to trace back to a middleware.
func TestStatusWriterSupportsFlush(t *testing.T) {
	var flushed bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("the wrapped writer does not implement http.Flusher")
		}
		_, _ = w.Write([]byte("event: ping\n\n"))
		f.Flush()
		flushed = true
	})

	httpx.Chain(handler, middleware.Observe(true, "", nil)).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil))

	if !flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}
