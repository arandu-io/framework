package scheduler

import (
	"context"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/kernel"
)

// Module runs the scheduler in the application process.
//
// It is registered like any other module and collects the tasks from the ones
// registered before it -- which is why it goes last in the Register call. A
// module never starts its own goroutine; it declares work, and this is what
// runs it.
type Module struct {
	tasks     []kernel.Task
	opts      Options
	scheduler *Scheduler
}

// NewModule returns the module for a set of tasks.
//
// Use kernel.Tasks() to collect them from the registered modules:
//
//	k := kernel.New(cfg).Register(billing.New(...), reports.New(...))
//	k.Register(scheduler.NewModule(k.Tasks(), scheduler.Options{
//	    Locker:  kv.NewLocker(client),
//	    Tenants: tenants.Active,
//	}))
func NewModule(tasks []kernel.Task, opts Options) *Module {
	return &Module{tasks: tasks, opts: opts}
}

var (
	_ kernel.Module     = (*Module)(nil)
	_ kernel.Bootable   = (*Module)(nil)
	_ kernel.Closable   = (*Module)(nil)
	_ kernel.Diagnostic = (*Module)(nil)
)

// Name is the module identifier.
func (*Module) Name() string { return "scheduler" }

// Routes registers nothing. A scheduled task is not reachable over HTTP, and
// making it reachable would be a way to trigger billing by URL.
func (*Module) Routes(*httpx.Router) {}

// Boot parses the tasks and starts the loop.
//
// An unparseable spec fails the boot. The application does not start with a
// task that would silently never run, which is what "config validated at boot,
// fail fast" means applied to schedules.
func (m *Module) Boot(ctx context.Context) error {
	if len(m.tasks) == 0 {
		return nil
	}

	s, err := New(m.tasks, m.opts)
	if err != nil {
		return err
	}
	m.scheduler = s
	s.Start(ctx)
	return nil
}

// Close stops the loop and waits for the run in flight.
func (m *Module) Close(ctx context.Context) error {
	if m.scheduler == nil {
		return nil
	}
	return m.scheduler.Stop(ctx)
}

// Diagnose reports overdue and failed tasks on the error page.
func (m *Module) Diagnose(ctx context.Context) []string {
	if m.scheduler == nil {
		return nil
	}
	return m.scheduler.Diagnose(ctx)
}

// Scheduler returns the running scheduler, for `aru schedule:list` and
// `aru schedule:run` to reach through the application binary.
//
// Nil before Boot.
func (m *Module) Scheduler() *Scheduler { return m.scheduler }
