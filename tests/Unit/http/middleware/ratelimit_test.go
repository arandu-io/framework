package unit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/hesape/cache"
	hlog "github.com/arandu-io/hesape/log"
	rmiddleware "github.com/arandu-io/hesape/routing/middleware"
)

// throttled builds the limit exactly as an application wires it: the counter
// over a store, the key from this package, and this package's Refuse as the
// answer. Everything below is measured through that wiring and not through a
// stub, because the two halves this package still owns -- the key and the
// refusal -- are only exercised when they are the ones passed in.
func throttled(t *testing.T, store cache.Store, limit cache.Limit, key func(*http.Request) string) http.Handler {
	t.Helper()
	return fhttp.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		rmiddleware.Throttle(cache.NewRateLimiter(store), limit, key, fhttp.Refuse),
	)
}

// TestTheLimitLimitsAtTheBoundaryAndPastIt walks a budget to its last unit and
// one past it. The boundary is the interesting number: an off-by-one either
// refuses somebody who was inside their limit or lets one extra request
// through on the endpoint the limit was put there for.
func TestTheLimitLimitsAtTheBoundaryAndPastIt(t *testing.T) {
	h := throttled(t, cache.NewArrayStore(), cache.PerMinute(2), middleware.KeyByIP)

	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200: it is inside the budget", i, rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != strconv.Itoa(2-i) {
			t.Errorf("remaining after request %d = %q, want %d", i, got, 2-i)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the request past the budget = %d, want 429", rec.Code)
	}
	// Retry-After must be usable: a client that honors it needs a positive
	// number of seconds, and truncation of a sub-second remainder gives zero.
	after, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || after < 1 {
		t.Fatalf("Retry-After = %q, want at least 1 second", rec.Header().Get("Retry-After"))
	}
}

// TestTheLimitIsPerKey: one caller must not be able to spend another's budget,
// which is the difference between a rate limit and an outage anybody can cause.
func TestTheLimitIsPerKey(t *testing.T) {
	h := throttled(t, cache.NewArrayStore(), cache.PerMinute(1), middleware.KeyByIP)

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

// TestTwoProcessesShareOneBudget is the whole reason the counter moved out of
// this package. The limiter that used to live here counted in process memory,
// so two replicas behind a load balancer allowed twice the limit -- on the
// endpoints the limit exists to protect. Two limiters over one store are one
// budget.
func TestTwoProcessesShareOneBudget(t *testing.T) {
	store := cache.NewArrayStore()
	replicaA := throttled(t, store, cache.PerMinute(1), middleware.KeyByIP)
	replicaB := throttled(t, store, cache.PerMinute(1), middleware.KeyByIP)

	rec := httptest.NewRecorder()
	replicaA.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the first replica = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	replicaB.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the second replica = %d, want 429: the budget was already spent, and a limit that "+
			"resets per process is not a limit", rec.Code)
	}
}

// brokenStore is a store nothing can be counted in. Only Increment fails,
// because Increment is what counting an attempt does: a store that failed on
// every method would prove the same branch while leaving it unclear which call
// reached it.
type brokenStore struct{ cache.Store }

func (brokenStore) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("the store is unreachable")
}

// TestARequestIsLetThroughWhenTheCounterCannotBeReached fixes what a caller
// sees when the store is down: the request is served. A rate limiter is a guard
// against abuse, not a dependency of serving a page, and one that refused
// everything while its store was unreachable would turn a cache outage into a
// total one.
//
// The headers are absent on that path, and that is the honest answer: nothing
// was counted, so there is no remaining budget to report. A limit header
// invented for a request nobody counted would be a number that means nothing.
func TestARequestIsLetThroughWhenTheCounterCannotBeReached(t *testing.T) {
	reached := false
	h := fhttp.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }),
		rmiddleware.Throttle(cache.NewRateLimiter(brokenStore{}), cache.PerMinute(1),
			middleware.KeyByIP, fhttp.Refuse),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !reached {
		t.Fatal("the handler was never reached: a rate limiter whose store is down must not become an outage")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "" {
		t.Errorf("X-RateLimit-Remaining = %q on a request that was never counted", got)
	}
}

// TestFailingOpenSaysSoInTheLog is the other half of the test above. Letting a
// request through unlimited is only acceptable while somebody is told it
// happened; silent, it is a limit that has quietly stopped existing.
func TestFailingOpenSaysSoInTheLog(t *testing.T) {
	var written bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&written, &slog.HandlerOptions{Level: slog.LevelError}))

	h := fhttp.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		rmiddleware.Throttle(cache.NewRateLimiter(brokenStore{}), cache.PerMinute(1),
			middleware.KeyByIP, fhttp.Refuse),
	)

	r := httptest.NewRequest(http.MethodGet, "/inbox", nil)
	h.ServeHTTP(httptest.NewRecorder(), r.WithContext(hlog.Into(r.Context(), logger)))

	line := written.String()
	if !strings.Contains(line, "level=ERROR") {
		t.Fatalf("the log says %q, and a limit that stopped counting is not a lower level than ERROR", line)
	}
	if !strings.Contains(line, "the store is unreachable") {
		t.Errorf("the log says %q, and it does not carry the reason the counter failed", line)
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

// TestTheKeyIsTheSameStringOnBothSides is why these two functions are still
// declared here rather than deleted with the limiter they used to feed. The
// counter now lives in a shared store, keyed by this string: if this wrapper
// and the function it forwards to ever disagreed, every caller would be handed
// a fresh budget on the deploy that introduced the difference.
func TestTheKeyIsTheSameStringOnBothSides(t *testing.T) {
	for _, remote := range []string{"10.0.0.1:5555", "[2001:db8:abcd:1234::1]:443", "[fe80::1%eth0]:443", "not-an-address"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote

		if got, want := middleware.KeyByIP(r), rmiddleware.KeyByIP(r); got != want {
			t.Errorf("%s is keyed %q here and %q where it is counted", remote, got, want)
		}
	}

	idFrom := func(r *http.Request) string { return r.Header.Get("X-Test-Session") }
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Test-Session", "sess-1")

	if got, want := middleware.KeyBySession(idFrom)(r), rmiddleware.KeyBySession(idFrom)(r); got != want {
		t.Errorf("a session is keyed %q here and %q where it is counted", got, want)
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
// an htmx one, so the same limit answers the same refusal and the browser
// renders it. Nothing in the throttle itself decides this: it comes from the
// Refuse handed to it, which is why it is measured here, with the Refuse an
// application passes.
func TestSomebodyOverTheRateLimitIsToldSoInAShapeTheirScreenCanShow(t *testing.T) {
	h := throttled(t, cache.NewArrayStore(), cache.PerMinute(1), middleware.KeyByIP)

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
	h := throttled(t, cache.NewArrayStore(), cache.PerMinute(1), middleware.KeyByIP)
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
