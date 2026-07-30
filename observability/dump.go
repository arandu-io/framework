package observability

import (
	"context"
	"time"
)

// Dump is the equivalent of Laravel's dump(), with one difference that matters:
// it does not write to stdout and does not corrupt the HTML of the response. The
// value is recorded in the Collector and shown on the debug page.
//
// In production, where the Collector is nil, it is a no-op.
func Dump(ctx context.Context, label string, value any) {
	c := FromContext(ctx)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Dumps = append(c.Dumps, DumpRecord{
		Label:  label,
		Value:  value,
		Caller: caller(2),
		At:     time.Since(c.Start),
	})
}

// DumpDie records the value and aborts the request with the dump page. It
// panics with a sentinel the Recover middleware recognizes.
//
// Like Dump, it is a no-op in production -- so a forgotten call cannot take a
// production request down.
func DumpDie(ctx context.Context, label string, value any) {
	c := FromContext(ctx)
	if c == nil {
		return
	}
	Dump(ctx, label, value)
	panic(dumpDie{})
}

type dumpDie struct{}

func (dumpDie) Error() string { return "arandu: dump and die" }

// IsDumpDie identifies the sentinel so the Recover middleware renders the dump
// page instead of treating the panic as a real 500.
func IsDumpDie(v any) bool {
	_, ok := v.(dumpDie)
	return ok
}
