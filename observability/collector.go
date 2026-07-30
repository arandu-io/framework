// Package observability is the reason this framework exists.
//
// slog covers structured logging and OpenTelemetry covers production tracing.
// What is missing between the two is the development layer: the moment a request
// broke and you want the stack, the queries with their timing, what you dumped
// and what the framework thinks is wrong, on one screen.
//
// The Collector is that layer, and it is core rather than a plugin, so the error
// page knows the queries, the dumps and the routes without any extra install.
package observability

import (
	"context"
	"runtime"
	"sync"
	"time"
)

type (
	ctxCollectorKey struct{}
	ctxSlotKey      struct{}
)

// collectorSlot is a mutable reference to the request Collector.
//
// It exists because of the pipeline order. Recover must be the outermost
// middleware, but the Collector is created by Observe, which runs inside it. The
// context Recover holds while a panic unwinds is therefore the one from BEFORE
// the Collector existed, and without a slot the error page would show a stack and
// nothing else -- no queries, no dumps, which is the whole point of the page.
type collectorSlot struct {
	mu sync.Mutex
	c  *Collector
}

func (s *collectorSlot) set(c *Collector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = c
}

func (s *collectorSlot) get() *Collector {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c
}

// WithCollectorSlot reserves a place in the context for a Collector that a
// middleware further in will create. Recover installs it in development; outside
// development it is not installed at all, so production pays nothing for it.
func WithCollectorSlot(ctx context.Context) context.Context {
	if _, ok := ctx.Value(ctxSlotKey{}).(*collectorSlot); ok {
		return ctx
	}
	return context.WithValue(ctx, ctxSlotKey{}, &collectorSlot{})
}

// Collector accumulates everything that happened inside ONE request: queries,
// dumps, events and outbound HTTP calls.
//
// Cost: the Collector is only installed in the context in development or when
// the request carries an authorized tracing header. In production, without the
// header, FromContext returns nil and every Record method is a no-op on a nil
// receiver -- zero cost, not "low cost".
type Collector struct {
	mu        sync.Mutex
	Start     time.Time
	RequestID string

	Queries  []QueryRecord
	Dumps    []DumpRecord
	Events   []EventRecord
	External []ExternalRecord
}

// QueryRecord is one database call, with the file and line that issued it --
// which is the field that actually saves time when hunting an N+1.
type QueryRecord struct {
	SQL      string
	Args     []any
	Duration time.Duration
	Rows     int
	Caller   Frame
	Err      error
}

// DumpRecord is one Dump call, with its origin and its offset into the request.
type DumpRecord struct {
	Label  string
	Value  any
	Caller Frame
	At     time.Duration // offset since the start of the request
}

// EventRecord is one application event emitted during the request.
type EventRecord struct {
	Name    string
	Payload any
	At      time.Duration
}

// ExternalRecord is one outbound HTTP call.
type ExternalRecord struct {
	Method   string
	URL      string
	Status   int
	Duration time.Duration
}

// Frame is a source location.
type Frame struct {
	File string
	Line int
	Func string
}

// NewCollector returns a collector for a request id.
func NewCollector(requestID string) *Collector {
	return &Collector{Start: time.Now(), RequestID: requestID}
}

// WithCollector installs the collector in the context, and fills the slot when
// one was reserved upstream, so a middleware outside this one can still reach it.
func WithCollector(ctx context.Context, c *Collector) context.Context {
	if s, ok := ctx.Value(ctxSlotKey{}).(*collectorSlot); ok {
		s.set(c)
	}
	return context.WithValue(ctx, ctxCollectorKey{}, c)
}

// FromContext returns the request collector, or nil in production. Every method
// below is safe on a nil receiver, so callers never need to check.
func FromContext(ctx context.Context) *Collector {
	if c, ok := ctx.Value(ctxCollectorKey{}).(*Collector); ok {
		return c
	}
	if s, ok := ctx.Value(ctxSlotKey{}).(*collectorSlot); ok {
		return s.get()
	}
	return nil
}

// RecordQuery stores one database call. The skip value walks past this method
// and the data.DB wrapper, so Caller points at the repository, not at the
// framework.
func (c *Collector) RecordQuery(sql string, args []any, d time.Duration, rows int, err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Queries = append(c.Queries, QueryRecord{
		SQL: sql, Args: args, Duration: d, Rows: rows, Err: err, Caller: caller(3),
	})
}

// RecordEvent stores one application event.
func (c *Collector) RecordEvent(name string, payload any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Events = append(c.Events, EventRecord{Name: name, Payload: payload, At: time.Since(c.Start)})
}

// RecordExternal stores one outbound HTTP call.
func (c *Collector) RecordExternal(method, url string, status int, d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.External = append(c.External, ExternalRecord{Method: method, URL: url, Status: status, Duration: d})
}

// SlowQueries returns the queries at or above the limit. It feeds the "slow
// query" badge on the debug page.
func (c *Collector) SlowQueries(limit time.Duration) []QueryRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []QueryRecord
	for _, q := range c.Queries {
		if q.Duration >= limit {
			out = append(out, q)
		}
	}
	return out
}

// SuspectedNPlusOne counts identical statements repeated within the request and
// returns those at or above the threshold. It is the diagnosis that saves the
// most time on generated CRUD.
func (c *Collector) SuspectedNPlusOne(threshold int) map[string]int {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := map[string]int{}
	for _, q := range c.Queries {
		count[q.SQL]++
	}
	for sql, n := range count {
		if n < threshold {
			delete(count, sql)
		}
	}
	return count
}

// QueryTime is the total time spent in the database during the request.
func (c *Collector) QueryTime() time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var total time.Duration
	for _, q := range c.Queries {
		total += q.Duration
	}
	return total
}

func caller(skip int) Frame {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return Frame{}
	}
	f := Frame{File: file, Line: line}
	if fn := runtime.FuncForPC(pc); fn != nil {
		f.Func = fn.Name()
	}
	return f
}
