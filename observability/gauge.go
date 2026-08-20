package observability

import (
	"cmp"
	"slices"
	"sync"
)

// GaugeName identifies one reading: what is measured, and whose it is.
//
// A comparable struct rather than one formatted string, because a string key
// has to be taken apart again to answer "every tenant this metric was set for",
// and a metric or a tenant that contains the separator makes that answer wrong.
type GaugeName struct {
	// Metric is what is being measured. The registry never interprets it: the
	// caller that sets a name is the one that knows what it counts.
	Metric string
	// Tenant is whose number it is. Empty is the process as a whole, which is
	// what a number that cannot honestly be attributed to a tenant reads as.
	Tenant string
}

// Gauges holds the current value of numbers the process owns, one int64 per
// [GaugeName].
//
// It keeps exactly one reading per name. Set replaces what was there, and what
// was there is gone: no history, no window, no peak, no average and no rate. A
// reader gets what is true now, and there is nothing to expire, sample or page
// through.
//
// This is what the Collector and the Recorder are not. Both of those are scoped
// to one request and drop what they hold when it ends, so a number that belongs
// to the process rather than to a request fits in neither.
//
// The registry stores; it does not measure. Whatever keeps the number is what
// writes it here, and it is that writer, not this type, that knows what the
// number means.
//
// Safe for concurrent use.
type Gauges struct {
	mu     sync.RWMutex
	values map[GaugeName]int64
}

// NewGauges returns an empty registry. A name appears the first time it is Set.
func NewGauges() *Gauges {
	return &Gauges{values: make(map[GaugeName]int64)}
}

// Set records the current value of one name, replacing any value that name
// already had.
func (g *Gauges) Set(name GaugeName, value int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.values[name] = value
}

// Read returns the current value of one name, and whether that name has ever
// been Set.
//
// The second result is what separates a number sitting at zero from a number
// nothing has written yet, which on a screen are the same digit and different
// facts.
func (g *Gauges) Read(name GaugeName) (int64, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	value, ok := g.values[name]
	return value, ok
}

// Names returns every name the registry currently holds, ordered by metric and
// then by tenant.
//
// Sorted rather than in map order, because callers draw tables from this and a
// table that reshuffles itself on every reload is a table nobody can compare
// two readings of.
func (g *Gauges) Names() []GaugeName {
	g.mu.RLock()
	names := make([]GaugeName, 0, len(g.values))
	for name := range g.values {
		names = append(names, name)
	}
	g.mu.RUnlock()

	slices.SortFunc(names, func(a, b GaugeName) int {
		if c := cmp.Compare(a.Metric, b.Metric); c != 0 {
			return c
		}
		return cmp.Compare(a.Tenant, b.Tenant)
	})
	return names
}
