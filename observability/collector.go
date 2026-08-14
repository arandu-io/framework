// The Collector and its records, answered by github.com/arandu-io/hesape/log.
//
// Every type here is an alias, which is what makes the Collector one type
// rather than two: the one the data layer records a query on and the one the
// error page reads are the same pointer, whichever of the two names the caller
// spelled.

package observability

import (
	"context"

	hlog "github.com/arandu-io/hesape/log"
)

// Collector accumulates everything that happened inside ONE request: queries,
// dumps, events and outbound HTTP calls.
//
// Cost: the Collector is only installed in the context in development or when
// the request carries an authorized tracing header. In production, without the
// header, FromContext returns nil and every Record method is a no-op on a nil
// receiver -- zero cost, not "low cost".
//
// The alias carries the methods with it, so RecordQuery, SlowQueries,
// SuspectedNPlusOne and the rest are hesape's and are documented there.
type Collector = hlog.Collector

// QueryRecord is one database call, with the file and line that issued it.
type QueryRecord = hlog.QueryRecord

// DumpRecord is one Dump call, with its origin and its offset into the request.
type DumpRecord = hlog.DumpRecord

// EventRecord is one application event emitted during the request.
type EventRecord = hlog.EventRecord

// ExternalRecord is one outbound HTTP call.
type ExternalRecord = hlog.ExternalRecord

// RenderRecord is one template render.
type RenderRecord = hlog.RenderRecord

// Frame is a source location.
type Frame = hlog.Frame

// Timeline breaks the request duration down by where it was spent.
type Timeline = hlog.Timeline

// NewCollector returns a collector for a request id.
func NewCollector(requestID string) *Collector {
	return hlog.NewCollector(requestID)
}

// FromContext returns the request collector, or nil in production. Every method
// on it is safe on a nil receiver, so callers never need to check.
func FromContext(ctx context.Context) *Collector {
	return hlog.FromContext(ctx)
}

// WithCollector installs the collector in the context, and fills the slot when
// one was reserved upstream, so a middleware outside this one can still reach
// it.
func WithCollector(ctx context.Context, c *Collector) context.Context {
	return hlog.WithCollector(ctx, c)
}

// WithCollectorSlot reserves a place in the context for a Collector that a
// middleware further in will create. Recover installs it in development;
// outside development it is not installed at all, so production pays nothing
// for it.
func WithCollectorSlot(ctx context.Context) context.Context {
	return hlog.WithCollectorSlot(ctx)
}
