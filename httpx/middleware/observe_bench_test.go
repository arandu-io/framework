package middleware_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/httpx/middleware"
	"github.com/arandu-io/framework/observability"
)

// Exit criterion 2 of phase 3: the overhead of the Collector with tracing off
// has to be measurable and zero -- not low. Same binary, same middleware, the
// difference being only whether the Collector was installed.
//
// This is what makes "zero cost in production" a claim rather than a hope:
//
//	go test ./httpx/middleware -bench Observe -benchmem
//
// The number that matters is BenchmarkObserveProduction, which must not grow as
// the collected surface grows. It records queries the whole time and the
// Collector is nil, so every Record call is a method on a nil receiver that
// returns immediately -- no allocation, no lock, no slice.

// args and dumped exist outside the handler on purpose.
//
// Building them inside would measure the cost of building them, which is not
// what the criterion is about: on the real path they already exist -- data.DB
// passes the same slice it just handed to the driver, and a Dump call passes a
// value the handler already had. Allocating them here would put 20 allocations
// into the "instrumented" column that production never pays.
var (
	args   = []any{"c-1"}
	dumped = map[string]any{"path": "/customers"}
)

// work stands in for a handler that touches the database and dumps a value.
func work(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	col := observability.FromContext(ctx)

	for range 20 {
		col.RecordQuery("SELECT * FROM invoice WHERE customer_id = ?", args, time.Microsecond, 1, nil)
	}
	observability.Dump(ctx, "the request", dumped)
	col.RecordRender("customer/list.templ", time.Microsecond)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// BenchmarkObserveProduction: no dev, no tracing secret, no recorder. This is
// what a deployed binary runs on every request.
func BenchmarkObserveProduction(b *testing.B) {
	h := httpx.Chain(http.HandlerFunc(work), middleware.Observe(false, "", nil))
	benchmark(b, h)
}

// BenchmarkObserveDevelopment is the cost when the console is on, for
// comparison. It is expected to be much larger -- twenty query records with
// their stack frames -- and that is fine, because it only happens on a laptop.
func BenchmarkObserveDevelopment(b *testing.B) {
	h := httpx.Chain(http.HandlerFunc(work), middleware.Observe(true, "", observability.NewRecorder(200)))
	benchmark(b, h)
}

// BenchmarkObserveProductionUninstrumented is the comparison that settles the
// criterion.
//
// It is the same pipeline and the same environment as
// BenchmarkObserveProduction, with one difference: the handler records nothing.
// Whatever separates the two IS the cost of instrumenting the code, measured
// with tracing off -- and it has to be indistinguishable from noise, because a
// Record on a nil Collector returns before touching anything.
//
// The other benchmarks measure the request id, the scoped logger and the access
// log, which every environment pays and which are not what this criterion is
// about.
func BenchmarkObserveProductionUninstrumented(b *testing.B) {
	h := httpx.Chain(http.HandlerFunc(bareWork), middleware.Observe(false, "", nil))
	benchmark(b, h)
}

// bareWork is work() with the instrumentation deleted.
func bareWork(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// BenchmarkNoMiddleware is the floor: the instrumented handler with nothing
// wrapped around it.
func BenchmarkNoMiddleware(b *testing.B) {
	benchmark(b, http.HandlerFunc(work))
}

func benchmark(b *testing.B, h http.Handler) {
	b.Helper()

	// The access log is written on every request in every environment, and
	// writing it to the terminal would be measuring the terminal. Discarding it
	// keeps the log formatting in the measurement, which is honest -- production
	// pays that cost too, it just pays it into a file or a collector.
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h = httpx.Chain(h, observability.RootLogger(quiet))

	r := httptest.NewRequest(http.MethodGet, "/customers", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}

// TestTheCollectorIsAbsentInProduction is the assertion behind the benchmark: a
// number is only meaningful if the thing it measures is really off.
func TestTheCollectorIsAbsentInProduction(t *testing.T) {
	var installed bool
	h := httpx.Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		installed = observability.FromContext(r.Context()) != nil
	}), middleware.Observe(false, "", nil))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if installed {
		t.Fatal("the Collector was installed in production")
	}
}

// TestNothingIsRecordedWithoutARecorder: passing nil is what a production
// pipeline does, and it must not be a special case anyone has to remember.
func TestNothingIsRecordedWithoutARecorder(t *testing.T) {
	h := httpx.Chain(http.HandlerFunc(work), middleware.Observe(false, "", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// TestTheTracingHeaderTurnsItOnForOneRequest: production tracing on demand,
// which is the point of the header. Without the header the same binary records
// nothing.
func TestTheTracingHeaderTurnsItOnForOneRequest(t *testing.T) {
	recorder := observability.NewRecorder(10)
	h := httpx.Chain(http.HandlerFunc(work), middleware.Observe(false, "s3cret", recorder))

	plain := httptest.NewRequest(http.MethodGet, "/customers", nil)
	h.ServeHTTP(httptest.NewRecorder(), plain)
	if recorder.Len() != 0 {
		t.Fatal("a request without the header was recorded")
	}

	traced := httptest.NewRequest(http.MethodGet, "/customers", nil)
	traced.Header.Set("X-Arandu-Trace", "s3cret")
	h.ServeHTTP(httptest.NewRecorder(), traced)
	if recorder.Len() != 1 {
		t.Fatalf("the traced request was not recorded: %d entries", recorder.Len())
	}

	// A wrong secret is the interesting case: it must behave exactly like no
	// header at all, or the endpoint becomes an oracle for guessing it.
	wrong := httptest.NewRequest(http.MethodGet, "/customers", nil)
	wrong.Header.Set("X-Arandu-Trace", "not-the-secret")
	h.ServeHTTP(httptest.NewRecorder(), wrong)
	if recorder.Len() != 1 {
		t.Fatal("a wrong tracing secret was accepted")
	}
}

// TestAnEmptySecretDoesNotEnableTracing: the zero value of the configuration
// must not be the one that turns production tracing on for anybody who sends an
// empty header.
func TestAnEmptySecretDoesNotEnableTracing(t *testing.T) {
	recorder := observability.NewRecorder(10)
	h := httpx.Chain(http.HandlerFunc(work), middleware.Observe(false, "", recorder))

	r := httptest.NewRequest(http.MethodGet, "/customers", nil)
	r.Header.Set("X-Arandu-Trace", "")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if recorder.Len() != 0 {
		t.Fatal("an empty tracing secret enabled the Collector")
	}
}
