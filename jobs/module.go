// The module, which is the one file here that is not a translation of names.
//
// hesape/queue has a Module of its own and everything it decides is kept: the
// lag thresholds, the sentences the error page prints, and the rule that the
// schema is asked of the driver rather than answered for it. What could not be
// kept is a single method. Routes names a *routing.Router there and a
// *http.Router here, and those are two types, so the hesape module cannot be
// registered through the contract this framework collects.
//
// This is an envelope over that one method and nothing else. Every other answer
// is the inner module's, so there is one description of when a queue counts as
// stalled rather than two that can drift apart.

package jobs

import (
	"context"

	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	hqueue "github.com/arandu-io/hesape/queue"
)

// Module brings the queue's schema, and reports on whether anything is draining
// it.
//
// It registers no routes and runs no worker: the worker is `aru work`, a
// separate process from the same image. What this module owns is the schema and
// the answer to "is anything draining this". Register it in bootstrap/app.go
// next to the modules that push jobs.
//
// # Why this one is declared and not aliased
//
// One method stands in the way. hesape/queue.Module answers Routes with a
// *routing.Router and this one answers it with a *http.Router, so the hesape
// module does not satisfy the contract the kernel collects. Everything else is
// the inner module's, down to the lag thresholds and the sentences the error
// page prints, so there is one description of a stalled queue and not two that
// can drift apart.
type Module struct {
	inner *hqueue.Module
}

// NewModule returns the module for a queue driver.
//
// Name the queues the application actually uses. A queue nobody watches is a
// queue that can stop draining without anyone noticing, which is the failure
// this module exists to make visible. Naming none watches the default queue.
//
// The driver is passed through rather than wrapped, because the schema is
// discovered by asking it: a driver that keeps jobs in the application's
// database carries the table it reads, and one that keeps them elsewhere
// carries nothing. Anything standing between the two would have to answer that
// question for the driver, and would answer it wrong for every driver it did
// not know about.
func NewModule(q hqueue.Queue, queues ...string) *Module {
	return &Module{inner: hqueue.NewModule(q, queues...)}
}

var (
	_ kernel.Module     = (*Module)(nil)
	_ kernel.Migratable = (*Module)(nil)
	_ kernel.Health     = (*Module)(nil)
	_ kernel.Diagnostic = (*Module)(nil)
)

// Name is the module identifier.
//
// The inner module answers, so the name a migration is collected under and the
// name a failing health check prints are one string and not two.
func (m *Module) Name() string { return m.inner.Name() }

// Routes registers nothing: this module has no HTTP surface.
//
// It is the method that could not be delegated, and the whole reason this type
// exists: the router this signature names is not the one the inner module's
// signature names.
func (*Module) Routes(*http.Router) {}

// Migrations returns the schema the wired driver needs.
//
// A driver that stores jobs outside the application's database returns nothing
// here, rather than an empty table it will never read.
func (m *Module) Migrations() []kernel.Migration { return m.inner.Migrations() }

// Health fails when a queue stops draining.
//
// A stopped worker looks exactly like an idle one, and the age of the oldest
// waiting job is what tells them apart. Without this, the first sign is a
// customer asking why they never got the email.
func (m *Module) Health(ctx context.Context) error { return m.inner.Health(ctx) }

// Diagnose reports a backlog and a dead letter queue, on the error page.
//
// Next to the failure somebody is already looking at, which is the moment they
// are most likely to act on it.
func (m *Module) Diagnose(ctx context.Context) []string { return m.inner.Diagnose(ctx) }
