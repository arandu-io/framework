// Dump and dump-and-die, answered by github.com/arandu-io/hesape/log.
//
// The sentinel DumpDie panics with is hesape's, and IsDumpDie asks hesape about
// it, so the two still agree: the pair cannot drift apart the way two copies of
// an unexported type would.

package observability

import (
	"context"

	hlog "github.com/arandu-io/hesape/log"
)

// Dump records a value for the debug page. It is the print statement you reach
// for while chasing something, with the difference that matters: it does not
// write to stdout and does not corrupt the HTML of the response. The value is
// recorded in the Collector and shown on the debug page.
//
// In production, where the Collector is nil, it is a no-op.
func Dump(ctx context.Context, label string, value any) {
	hlog.Dump(ctx, label, value)
}

// DumpDie records the value and aborts the request with the dump page. It
// panics with a sentinel the Recover middleware recognizes.
//
// The die half is not conditional, and that is a behaviour change a caller can
// see. Returning without panicking when there is no Collector -- which is every
// request outside development -- makes a forgotten call answer 200 with the dump
// written into the middle of the body, a page broken in a way nothing reports.
// It aborts wherever it is called, and outside development Recover logs it by
// name before answering the error page. The recording half still needs a
// Collector and still does nothing without one.
func DumpDie(ctx context.Context, label string, value any) {
	hlog.DumpDie(ctx, label, value)
}

// IsDumpDie identifies the sentinel so the Recover middleware renders the dump
// page instead of treating the panic as a real 500.
func IsDumpDie(v any) bool {
	return hlog.IsDumpDie(v)
}
