// The route rate limit. The two key functions are answered by
// github.com/arandu-io/hesape/routing/middleware; the limiter and the
// middleware around it are NOT, and the comments below say why.

package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	rmiddleware "github.com/arandu-io/hesape/routing/middleware"
)

// Limiter is the rate limit backend. The core ships the in-memory
// implementation only; the redis adapter provides the distributed one.
//
// It stays declared here rather than pointing at hesape. The replacement there
// is hesape/routing/middleware.Throttle, which takes a CONCRETE
// *hesape/cache.RateLimiter rather than an interface, and
// github.com/arandu-io/kv implements this interface by these method names in a
// separate module (kv/limiter.go:29 asserts it). A separate module cannot
// satisfy a concrete struct, so aliasing here would compile in the framework
// and break the adapter silently -- the one failure `go build` in this module
// cannot catch.
type Limiter interface {
	Allow(key string, limit int, window time.Duration) (remaining int, retryAfter time.Duration, ok bool)
}

// RateLimit limits by key.
//
// Use KeyByIP for public routes and KeyBySession for authenticated ones:
// limiting login attempts by IP alone does not stop distributed credential
// stuffing, because every attempt arrives from a different address.
//
// The refusal goes through fhttp.Refuse, like the role guard's 403 and
// CSRFProtect's 419. This is the third of the three middlewares in this package
// that turn a request away, and http.Error is not enough for it: htmx swaps no
// 4xx, so somebody who has hit the limit presses the button and the screen does
// not change at all -- on the one refusal that arrives when a person is already
// pressing repeatedly. Refusing in two shapes is the inconsistency rather than
// the fix.
//
// The sentence carries the same number as Retry-After, computed once, because a
// header and a sentence that disagree about how long to wait are worse than one
// that says nothing.
//
// It is not a bridge. hesape/routing/middleware.Throttle is the same
// middleware over a different contract -- a *cache.RateLimiter and a
// cache.Limit instead of this interface, a budget and a window -- and it fails
// open where this one has no failure to answer, because Allow returns no error.
// Translating between the two would mean inventing an answer for a store that
// cannot be reached, on behalf of every caller, which is a behaviour change
// wearing a bridge's clothes. It stays here for as long as Limiter does.
func RateLimit(l Limiter, limit int, window time.Duration, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remaining, retry, ok := l.Allow(key(r), limit, window)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !ok {
				seconds := strconv.Itoa(int(retry.Seconds()) + 1)
				w.Header().Set("Retry-After", seconds)
				fhttp.Refuse(w, r, http.StatusTooManyRequests,
					"too many requests: wait "+seconds+" seconds and try again")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyByIP keys on the peer address: the whole address over IPv4, and the /64 it
// sits in over IPv6.
//
// It reads RemoteAddr and never X-Forwarded-For: a header the client controls is
// a way to reset someone else's counter. Behind a proxy, have the proxy rewrite
// RemoteAddr, or key on something the proxy signs. A proxy that does neither
// gives every request in the world the same key, and then every limit keyed this
// way is a limit on the whole application -- which for the sign-in throttle
// means twenty-five wrong passwords a minute across every customer.
//
// # Why the IPv6 address is masked
//
// Because otherwise it is not a limit. IPv4 addresses are scarce, so keying on
// the whole address costs an attacker money; a /64 is the smallest block any end
// site is given -- a home connection, a VPS, a phone -- and every one of them
// holds eighteen quintillion addresses that all reach this server. Keyed on the
// full address, one machine with a routed /64 had an unlimited number of
// budgets: it could walk a list of accounts forever, and fill the sign-in
// throttle's table on its own, from a single upstream link.
//
// The /64 and not something wider, because it is the one boundary that is
// always a single link. A /48 would be one customer at some providers and a
// whole building at others, and grouping two subscribers under one budget is
// how a limit locks out somebody who did nothing.
//
// A wrapper and not an alias, because a plain function has no alias form. The
// key it returns is byte-for-byte the one this package produced before the
// move, which matters: a counter in a shared store is keyed by this string, and
// a different prefix would hand every caller a fresh budget on deploy.
func KeyByIP(r *http.Request) string { return rmiddleware.KeyByIP(r) }

// KeyBySession keys on the session id, falling back to the address for
// anonymous requests. Pass SessionStore.IDFromRequest as the extractor.
//
// The declared return type stays func(*http.Request) string rather than
// hesape's named KeyFunc, so the signature every caller is written against is
// unchanged; the value returned is the same function either way.
func KeyBySession(idFrom func(*http.Request) string) func(*http.Request) string {
	return rmiddleware.KeyBySession(idFrom)
}

// MemoryLimiter is a fixed window in process memory. It is right for
// development and for a single instance. Behind more than one pod a window per
// pod limits nothing -- use the redis adapter there.
type MemoryLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	// lastSweep bounds memory: without it every distinct key ever seen stays in
	// the map forever, which is a slow leak driven by anyone who can send a
	// request.
	lastSweep time.Time
}

type bucket struct {
	count int
	reset time.Time
}

// NewMemoryLimiter returns an empty in-memory limiter.
func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{buckets: map[string]*bucket{}, lastSweep: time.Now()}
}

// Allow consumes one unit from the key's window.
func (m *MemoryLimiter) Allow(key string, limit int, window time.Duration) (int, time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if now.Sub(m.lastSweep) > window {
		for k, b := range m.buckets {
			if now.After(b.reset) {
				delete(m.buckets, k)
			}
		}
		m.lastSweep = now
	}

	b, exists := m.buckets[key]
	if !exists || now.After(b.reset) {
		b = &bucket{reset: now.Add(window)}
		m.buckets[key] = b
	}
	b.count++
	if b.count > limit {
		return 0, time.Until(b.reset), false
	}
	return limit - b.count, 0, true
}

// Len reports how many buckets are held. It exists so a test can prove the sweep
// actually bounds memory.
func (m *MemoryLimiter) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.buckets)
}
