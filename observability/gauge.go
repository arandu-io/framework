// The gauge registry, answered by github.com/arandu-io/hesape/log.

package observability

import hlog "github.com/arandu-io/hesape/log"

// GaugeName identifies one reading: what is measured, and whose it is.
//
// A comparable struct rather than one formatted string, because a string key
// has to be taken apart again to answer "every tenant this metric was set for",
// and a metric or a tenant that contains the separator makes that answer wrong.
type GaugeName = hlog.GaugeName

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
type Gauges = hlog.Gauges

// NewGauges returns an empty registry. A name appears the first time it is Set.
func NewGauges() *Gauges {
	return hlog.NewGauges()
}
