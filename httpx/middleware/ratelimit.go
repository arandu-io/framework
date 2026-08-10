package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/arandu-io/framework/httpx"
)

// Limiter is the rate limit backend. The core ships the in-memory
// implementation only; the redis adapter provides the distributed one.
type Limiter interface {
	Allow(key string, limit int, window time.Duration) (remaining int, retryAfter time.Duration, ok bool)
}

// RateLimit limits by key.
//
// Use KeyByIP for public routes and KeyBySession for authenticated ones:
// limiting login attempts by IP alone does not stop distributed credential
// stuffing, because every attempt arrives from a different address.
//
// The refusal goes through httpx.Refuse, like the role guard's 403 and
// CSRFProtect's 419. This is the third of the three middlewares in this package
// that turn a request away, and it was the one left on http.Error: htmx swaps no
// 4xx, so somebody who had hit the limit pressed the button and the screen did
// not change at all -- the same failure the other two were fixed for, on the one
// refusal that arrives when a person is already pressing repeatedly. Refusing in
// two shapes is the inconsistency rather than the fix (RULE 9).
//
// The sentence carries the same number as Retry-After, computed once, because a
// header and a sentence that disagree about how long to wait are worse than one
// that says nothing.
func RateLimit(l Limiter, limit int, window time.Duration, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remaining, retry, ok := l.Allow(key(r), limit, window)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !ok {
				seconds := strconv.Itoa(int(retry.Seconds()) + 1)
				w.Header().Set("Retry-After", seconds)
				httpx.Refuse(w, r, http.StatusTooManyRequests,
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
func KeyByIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// An unparseable address is used as it stands rather than dropped: it is the
	// only key left, and merging every one of them into a shared bucket would be
	// a way to be limited by somebody else's traffic.
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "ip:" + host
	}
	if addr.Is4() || addr.Is4In6() {
		return "ip:" + addr.Unmap().String()
	}
	// WithZone("") because a link-local scope is the receiving interface's name,
	// not the sender's, and it would key two arrivals of the same address apart.
	block, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return "ip:" + addr.String()
	}
	return "ip:" + block.String()
}

// KeyBySession keys on the session id, falling back to the address for
// anonymous requests. Pass SessionStore.IDFromRequest as the extractor.
func KeyBySession(idFrom func(*http.Request) string) func(*http.Request) string {
	return func(r *http.Request) string {
		if id := idFrom(r); id != "" {
			return "session:" + id
		}
		return KeyByIP(r)
	}
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
