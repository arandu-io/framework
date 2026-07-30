package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/httpx/middleware"
)

func TestRateLimitBlocksOverTheLimit(t *testing.T) {
	limiter := middleware.NewMemoryLimiter()
	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		middleware.RateLimit(limiter, 2, time.Minute, middleware.KeyByIP),
	)

	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i, rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != strconv.Itoa(2-i) {
			t.Errorf("remaining after request %d = %q, want %d", i, got, 2-i)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request = %d, want 429", rec.Code)
	}
	// Retry-After must be usable: a client that honors it needs a positive
	// number of seconds, and truncation of a sub-second window gives zero.
	after, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || after < 1 {
		t.Fatalf("Retry-After = %q, want at least 1 second", rec.Header().Get("Retry-After"))
	}
}

func TestRateLimitIsPerKey(t *testing.T) {
	limiter := middleware.NewMemoryLimiter()
	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		middleware.RateLimit(limiter, 1, time.Minute, middleware.KeyByIP),
	)

	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "10.0.0.1:1234"
	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "10.0.0.2:1234"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("first address = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, second)
	if rec.Code != http.StatusOK {
		t.Fatalf("second address = %d, want 200: one client must not spend another client's budget", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, first)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("first address again = %d, want 429", rec.Code)
	}
}

func TestWindowResets(t *testing.T) {
	limiter := middleware.NewMemoryLimiter()

	if _, _, ok := limiter.Allow("k", 1, 10*time.Millisecond); !ok {
		t.Fatal("first call was rejected")
	}
	if _, _, ok := limiter.Allow("k", 1, 10*time.Millisecond); ok {
		t.Fatal("second call inside the window was allowed")
	}

	time.Sleep(15 * time.Millisecond)

	if _, _, ok := limiter.Allow("k", 1, 10*time.Millisecond); !ok {
		t.Fatal("the window did not reset")
	}
}

// TestKeyByIPIgnoresForwardedFor: a header the client controls must never pick
// the bucket, or resetting someone else's counter is a one-line curl.
func TestKeyByIPIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := middleware.KeyByIP(r); got != "ip:10.0.0.1" {
		t.Fatalf("KeyByIP = %q, want ip:10.0.0.1", got)
	}
}

func TestKeyBySessionFallsBackToTheAddress(t *testing.T) {
	key := middleware.KeyBySession(func(r *http.Request) string {
		return r.Header.Get("X-Test-Session")
	})

	authenticated := httptest.NewRequest(http.MethodGet, "/", nil)
	authenticated.Header.Set("X-Test-Session", "sess-1")
	if got := key(authenticated); got != "session:sess-1" {
		t.Fatalf("authenticated key = %q, want session:sess-1", got)
	}

	anonymous := httptest.NewRequest(http.MethodGet, "/", nil)
	anonymous.RemoteAddr = "10.0.0.9:1"
	if got := key(anonymous); got != "ip:10.0.0.9" {
		t.Fatalf("anonymous key = %q, want ip:10.0.0.9", got)
	}
}

// TestMemoryLimiterSweepsExpiredBuckets: without a sweep, every distinct key ever
// seen stays in the map forever, which is a slow leak anyone can drive.
func TestMemoryLimiterSweepsExpiredBuckets(t *testing.T) {
	limiter := middleware.NewMemoryLimiter()
	window := 10 * time.Millisecond

	for i := range 100 {
		limiter.Allow("key-"+strconv.Itoa(i), 10, window)
	}
	time.Sleep(2 * window)
	// Any call after the window triggers the sweep of what expired.
	limiter.Allow("trigger", 10, window)

	if n := limiter.Len(); n > 2 {
		t.Fatalf("buckets retained = %d, want the expired ones gone", n)
	}
}
