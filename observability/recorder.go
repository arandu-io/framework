// The recorder behind the debug console, answered by
// github.com/arandu-io/hesape/log.

package observability

import hlog "github.com/arandu-io/hesape/log"

// DefaultRecorderSize is how many requests the console remembers.
//
// Two hundred is enough to cover the reload-look-reload loop of a debugging
// session and small enough that nobody has to think about the memory. Each
// entry holds the queries, dumps and events of one request, so a page that
// issues a hundred queries is the one that costs.
const DefaultRecorderSize = hlog.DefaultRecorderSize

// Recorded is one finished request, kept for the console.
type Recorded = hlog.Recorded

// Recorder is the ring buffer behind /_arandu/debug.
//
// A ring rather than a growing slice, because the alternative is a debug
// console that turns a long-running dev server into an out-of-memory kill --
// and it would happen at the end of a long session, which is exactly when
// losing the process costs the most.
//
// Every method is safe on a nil receiver. In production there is no recorder,
// and the middleware should not have to check.
type Recorder = hlog.Recorder

// NewRecorder returns a ring buffer of the given size. A size of zero or less
// takes the default.
func NewRecorder(size int) *Recorder {
	return hlog.NewRecorder(size)
}
