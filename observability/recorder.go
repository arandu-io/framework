package observability

import (
	"sync"
	"time"
)

// DefaultRecorderSize is how many requests the console remembers.
//
// Two hundred is enough to cover the reload-look-reload loop of a debugging
// session and small enough that nobody has to think about the memory. Each
// entry holds the queries, dumps and events of one request, so a page that
// issues a hundred queries is the one that costs.
const DefaultRecorderSize = 200

// Recorded is one finished request, kept for the console.
type Recorded struct {
	RequestID string
	Method    string
	Path      string
	Status    int
	// Duration is the wall clock of the whole request, which is what the
	// timeline is a breakdown of.
	Duration time.Duration
	// At is when the request started, so the list can be read in order.
	At time.Time
	// Collector holds everything recorded during the request. It is the same
	// pointer the request used: the request is over, so nothing writes to it
	// any more.
	Collector *Collector
}

// Recorder is the ring buffer behind /_arandu/debug.
//
// A ring rather than a growing slice, because the alternative is a debug
// console that turns a long-running dev server into an out-of-memory kill --
// and it would happen at the end of a long session, which is exactly when
// losing the process costs the most.
//
// Every method is safe on a nil receiver. In production there is no recorder,
// and the middleware should not have to check.
type Recorder struct {
	mu      sync.RWMutex
	entries []Recorded
	// next is where the following entry goes; the buffer wraps.
	next int
	// count stops at len(entries) once the ring has wrapped.
	count int
}

// NewRecorder returns a ring buffer of the given size. A size of zero or less
// uses DefaultRecorderSize.
func NewRecorder(size int) *Recorder {
	if size <= 0 {
		size = DefaultRecorderSize
	}
	return &Recorder{entries: make([]Recorded, size)}
}

// Record stores a finished request, discarding the oldest when full.
func (r *Recorder) Record(e Recorded) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[r.next] = e
	r.next = (r.next + 1) % len(r.entries)
	if r.count < len(r.entries) {
		r.count++
	}
}

// Recent returns the stored requests, newest first. A limit of zero or less
// returns all of them.
func (r *Recorder) Recent(limit int) []Recorded {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 || limit > r.count {
		limit = r.count
	}
	out := make([]Recorded, 0, limit)
	for i := range limit {
		// Walk backwards from the last written slot, wrapping into the tail.
		idx := (r.next - 1 - i + len(r.entries)*2) % len(r.entries)
		out = append(out, r.entries[idx])
	}
	return out
}

// Find returns one request by id.
//
// This is what `aru trace` resolves: you have a request id from a log line or
// from the X-Request-ID header, and you want everything that happened inside it.
func (r *Recorder) Find(requestID string) (Recorded, bool) {
	if r == nil || requestID == "" {
		return Recorded{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i := range r.count {
		idx := (r.next - 1 - i + len(r.entries)*2) % len(r.entries)
		if r.entries[idx].RequestID == requestID {
			return r.entries[idx], true
		}
	}
	return Recorded{}, false
}

// Len is how many requests are stored.
func (r *Recorder) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Timeline is where a request spent its time.
//
// The question it answers is the first one worth asking about a slow page: was
// it the database, the rendering, something it called, or the code itself.
// Without the breakdown, "this page takes 800ms" leads to guessing.
type Timeline struct {
	Total    time.Duration
	SQL      time.Duration
	Render   time.Duration
	External time.Duration
	// Other is what is left: application code, serialization, the framework
	// itself. A large Other with few queries is a CPU problem, and that is a
	// different investigation.
	Other time.Duration
}

// Percent returns d as a percentage of the total, rounded down. Zero total
// gives zero rather than a division by zero on a request that finished in under
// a microsecond.
func (t Timeline) Percent(d time.Duration) int {
	if t.Total <= 0 {
		return 0
	}
	return int(d * 100 / t.Total)
}

// Timeline breaks the request duration down by where it was spent.
func (c *Collector) Timeline(total time.Duration) Timeline {
	if c == nil {
		return Timeline{Total: total, Other: total}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// The unexported slices, not the accessors: sync.Mutex is not reentrant, and
	// an accessor called from inside the lock deadlocks the process. Every
	// method that already holds the lock reads the fields directly.
	t := Timeline{Total: total}
	for _, q := range c.queries {
		t.SQL += q.Duration
	}
	for _, r := range c.renders {
		t.Render += r.Duration
	}
	for _, e := range c.external {
		t.External += e.Duration
	}

	// The parts can exceed the total: queries issued from concurrent goroutines
	// overlap in wall clock. Reporting a negative Other would look like a bug in
	// the framework rather than like concurrency, so it clamps.
	if accounted := t.SQL + t.Render + t.External; accounted < total {
		t.Other = total - accounted
	}
	return t
}

// StatusClass groups a status code for the console, so the list can be scanned
// without reading every number.
func (e Recorded) StatusClass() string {
	switch {
	case e.Status >= 500:
		return "server-error"
	case e.Status >= 400:
		return "client-error"
	case e.Status >= 300:
		return "redirect"
	default:
		return "ok"
	}
}
