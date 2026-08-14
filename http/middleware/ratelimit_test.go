package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
)

func TestRateLimitBlocksOverTheLimit(t *testing.T) {
	limiter := middleware.NewMemoryLimiter()
	h := fhttp.Chain(
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
	h := fhttp.Chain(
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

// TestOneMachineWithARoutedBlockIsOneClient: over IPv6 the address is not a
// scarce resource. A single connection is handed a /64 -- eighteen quintillion
// addresses, all of which reach this server -- so a limit keyed on the whole
// address is a limit an attacker steps out of by picking a new source address
// for every request, which costs nothing.
func TestOneMachineWithARoutedBlockIsOneClient(t *testing.T) {
	key := func(remote string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		return middleware.KeyByIP(r)
	}

	first := key("[2001:db8:abcd:1234::1]:443")
	second := key("[2001:db8:abcd:1234:dead:beef:cafe:f00d]:9000")
	if first != second {
		t.Fatalf("two addresses out of one /64 are keyed %q and %q: every limit in the framework is off for "+
			"anyone with an IPv6 connection", first, second)
	}

	neighbour := key("[2001:db8:abcd:1235::1]:443")
	if neighbour == first {
		t.Fatalf("a different /64 shares the key %q: one subscriber's traffic would lock another's out", first)
	}
}

// TestAnAddressWithAScopeIsStillOneAddress: the zone on a link-local address
// names the interface it arrived on, which belongs to this server and not to the
// sender. Left in the key it would file the same machine under two buckets.
func TestAnAddressWithAScopeIsStillOneAddress(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[fe80::1%eth0]:443"
	scoped := middleware.KeyByIP(r)

	r.RemoteAddr = "[fe80::1%eth1]:443"
	if other := middleware.KeyByIP(r); other != scoped {
		t.Fatalf("the same address arriving on two interfaces is keyed %q and %q", scoped, other)
	}
}

// The rate limit is the third refusal in this package, and it refused in a shape
// htmx throws away: its response handling is `{code:"[45]..", swap:false,
// error:true}`, so the person pressed the button once too often and the screen
// did not change at all -- which reads as the application being broken, and the
// next thing anybody does about a button that did nothing is press it again.
//
// HX-Refresh reloads the page as an ordinary navigation, and that request is not
// an htmx one, so this same middleware answers the same refusal and the browser
// renders it.
func TestSomebodyOverTheRateLimitIsToldSoInAShapeTheirScreenCanShow(t *testing.T) {
	h := fhttp.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		middleware.RateLimit(middleware.NewMemoryLimiter(), 1, time.Minute, middleware.KeyByIP),
	)

	htmx := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/inbox/rows", nil)
		r.Header.Set("HX-Request", "true")
		return r
	}
	h.ServeHTTP(httptest.NewRecorder(), htmx())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, htmx())

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 -- the status is what a log and a probe read, and it does not change", rec.Code)
	}
	if rec.Header().Get("HX-Refresh") != "true" {
		t.Error("htmx was given nothing to do with this refusal, so the person clicked and nothing happened at all")
	}
}

// The other half: a browser that is not running htmx already renders the body,
// and telling it to reload would replace the sentence with the page the person
// was on and explain nothing.
func TestAPlainBrowserOverTheRateLimitIsNotToldToReload(t *testing.T) {
	h := fhttp.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		middleware.RateLimit(middleware.NewMemoryLimiter(), 1, time.Minute, middleware.KeyByIP),
	)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/inbox", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/inbox", nil))

	if got := rec.Header().Get("HX-Refresh"); got != "" {
		t.Errorf("HX-Refresh = %q on a request no htmx sent", got)
	}
	// The sentence and the header have to agree: two different numbers of
	// seconds is worse than one of them being absent.
	after := rec.Header().Get("Retry-After")
	if after == "" {
		t.Fatal("nothing told the client how long to wait")
	}
	if !strings.Contains(rec.Body.String(), "wait "+after+" seconds") {
		t.Errorf("the body says %q and Retry-After says %q", rec.Body.String(), after)
	}
}
