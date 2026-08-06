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

	// The slices are unexported and reachable only through the accessors below,
	// which copy under the lock. They used to be exported and read directly by
	// the console, the recorder, the worker and the scheduler -- every one of
	// those reads raced against a goroutine the handler had started and not
	// waited for, and the race detector says so. Found by audit.
	queries  []QueryRecord
	dumps    []DumpRecord
	events   []EventRecord
	external []ExternalRecord
	renders  []RenderRecord
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

// RenderRecord is one template render.
//
// It is what separates "the page is slow because of the database" from "the
// page is slow because of the view", which are two different afternoons.
type RenderRecord struct {
	Name     string
	Duration time.Duration
	At       time.Duration
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
	c.queries = append(c.queries, QueryRecord{
		SQL: sql, Args: args, Duration: d, Rows: rows, Err: err, Caller: caller(3),
	})
}

// RecordEvent stores one application event.
//
// Guard the call when the payload is a struct value:
//
//	if col := observability.FromContext(ctx); col != nil {
//	    col.RecordEvent("invoice.paid", invoice)
//	}
//
// This method is a no-op on a nil receiver, but converting a struct value to
// `any` allocates at the CALL SITE, before the receiver is ever looked at. So
// the unguarded form costs one heap allocation per event in production, where
// nothing will ever read it. A payload that is already a pointer, a map or a
// string boxes for free and needs no guard.
func (c *Collector) RecordEvent(name string, payload any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, EventRecord{Name: name, Payload: payload, At: time.Since(c.Start)})
}

// RecordExternal stores one outbound HTTP call.
func (c *Collector) RecordExternal(method, url string, status int, d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.external = append(c.external, ExternalRecord{Method: method, URL: url, Status: status, Duration: d})
}

// RecordRender stores one template render.
//
// porang calls it around a component; anything rendering HTML can call it too.
// The name is what shows on the timeline, so it should be the template, not the
// function.
func (c *Collector) RecordRender(name string, d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renders = append(c.renders, RenderRecord{Name: name, Duration: d, At: time.Since(c.Start)})
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
	for _, q := range c.queries {
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
	for _, q := range c.queries {
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
	for _, q := range c.queries {
		total += q.Duration
	}
	return total
}

// Queries returns a copy of the recorded database calls.
//
// A copy, and under the lock: the caller is usually the console rendering a
// request that has already finished, but a handler that started a goroutine and
// did not wait for it is still writing. Handing out the slice would hand out a
// race.
func (c *Collector) Queries() []QueryRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]QueryRecord(nil), c.queries...)
}

// Dumps returns a copy of the recorded dumps.
func (c *Collector) Dumps() []DumpRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]DumpRecord(nil), c.dumps...)
}

// Events returns a copy of the recorded application events.
func (c *Collector) Events() []EventRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]EventRecord(nil), c.events...)
}

// External returns a copy of the recorded outbound HTTP calls.
func (c *Collector) External() []ExternalRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ExternalRecord(nil), c.external...)
}

// Renders returns a copy of the recorded template renders.
func (c *Collector) Renders() []RenderRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]RenderRecord(nil), c.renders...)
}

// QueryCount is how many database calls the request made.
//
// It exists so the common case -- a log line saying how many -- does not copy
// the whole slice to call len on it.
func (c *Collector) QueryCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queries)
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
