package middleware

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
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
func RateLimit(l Limiter, limit int, window time.Duration, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remaining, retry, ok := l.Allow(key(r), limit, window)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyByIP keys on the peer address.
//
// It reads RemoteAddr and never X-Forwarded-For: a header the client controls is
// a way to reset someone else's counter. Behind a proxy, have the proxy rewrite
// RemoteAddr, or key on something the proxy signs.
func KeyByIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "ip:" + r.RemoteAddr
	}
	return "ip:" + host
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
