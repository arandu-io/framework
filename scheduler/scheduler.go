// The scheduler, answered by github.com/arandu-io/hesape/console/scheduling.
//
// The Scheduler is an envelope and not an alias, because the two designs meet a
// task from opposite ends. There, a schedule is declared through Schedule.Call
// and carried as an Event with its frequency, its filters and its mutex on it;
// here, a module declares a kernel.Task and the kernel collects it. The task
// carries three things an Event has no field for -- a Timeout, an
// observability.Recorder and a kernel.Locker -- and every one of the thirteen
// repositories is written against that shape.
//
// So the envelope declares one hesape CallbackEvent per kernel.Task and hands
// the run back. What crosses over is everything that is a schedule: which events
// are due in a minute, the expansion of a per-tenant event to one run per
// tenant, and the Grant each run carries. What stays is what belongs to the
// framework: the lock, the timeout, the Collector and the bookkeeping that
// `aru schedule:list` and the error page read.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/console/events"
	"github.com/arandu-io/hesape/console/scheduling"
)

// Tenants returns the tenants a PerTenant task expands to.
//
// Injected, because the core does not know where the application keeps its
// tenants -- a table, a config file, a control plane. Returning an empty list
// is valid and means the task simply does not run.
//
// An alias: the hesape type has the same name and the same shape, so a resolver
// written against either name satisfies both.
type Tenants = scheduling.Tenants

// Options configures the scheduler.
type Options struct {
	// Locker makes a Singleton task run on exactly one replica. Nil means a
	// single replica, and with more than one it means every replica runs
	// everything.
	//
	// It stays a kernel.Locker. hesape claims a window through a
	// SchedulingMutex, which marks the window and never releases it, where this
	// one wraps the run and releases at the end -- a lock that cannot be
	// expressed as the other. kernel.NewLocker builds one over a lock issuer.
	Locker kernel.Locker
	// Tenants expands PerTenant tasks. Nil means those tasks do not run, which
	// is reported rather than silent.
	Tenants Tenants
	// Now is the clock, for tests. Nil means time.Now.
	Now func() time.Time
	// Recorder receives each finished run, so the task shows on /_arandu/debug
	// with its queries and its timeline -- exactly like a request.
	//
	// Nil means no instrumentation, and that is what production looks like: no
	// Collector is built and every Record method is a no-op on a nil receiver.
	// Pass kernel.Recorder() to turn it on.
	Recorder *observability.Recorder
}

// entry is one task with the hesape event that fires it.
type entry struct {
	task  kernel.Task
	cron  Schedule
	event *scheduling.Event
	// lastRun and lastError are what Diagnose and `aru schedule:list` report.
	// hesape reports a run through its four scheduler events, which carry no
	// place to keep this.
	mu        sync.Mutex
	lastRun   time.Time
	lastError string
}

// Scheduler fires tasks on their schedule.
type Scheduler struct {
	entries  []*entry
	schedule *scheduling.Schedule
	opts     Options
	// stop cancels the loop, and done closes when it has stopped.
	stop context.CancelFunc
	done chan struct{}
}

// New parses the tasks and returns the scheduler.
//
// An unparseable spec is an error at construction rather than a task that
// silently never runs -- which is the failure mode of every scheduler that
// validates lazily.
func New(tasks []kernel.Task, opts Options) (*Scheduler, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}

	seen := map[string]bool{}
	entries := make([]*entry, 0, len(tasks))

	for _, t := range tasks {
		if t.ID == "" {
			return nil, errors.New("scheduler: a task with no id cannot be locked, listed or run by hand")
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("scheduler: two tasks share the id %q, and the lock cannot tell them apart", t.ID)
		}
		seen[t.ID] = true

		if t.Run == nil {
			return nil, fmt.Errorf("scheduler: %s has no Run", t.ID)
		}
		cron, err := Parse(t.Spec)
		if err != nil {
			return nil, fmt.Errorf("scheduler: %s: %w", t.ID, err)
		}
		if t.Timeout <= 0 {
			t.Timeout = 5 * time.Minute
		}
		entries = append(entries, &entry{task: t, cron: cron})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].task.ID < entries[j].task.ID })

	s := &Scheduler{entries: entries, opts: opts}

	// Both mutexes are nil. Overlap and one-server are attributes of a hesape
	// Event and not of a kernel.Task, and nothing here sets either, so neither
	// mutex is ever asked. The singleton lock is Options.Locker instead.
	s.schedule = scheduling.NewSchedule(nil, nil, nil)
	s.schedule.Tenants = opts.Tenants

	for _, e := range entries {
		event := s.schedule.Call(s.work(e))
		event.Name(e.task.ID)
		event.Cron(e.task.Spec)
		event.Action(e.task.Action)
		if e.task.Scope == kernel.PerTenant {
			event.PerTenant()
		}
		e.event = event.Event
	}

	return s, nil
}

// Start runs the loop until Stop.
//
// The loop stays here rather than delegating to scheduling.Module, which has one
// of its own: that loop calls the runner with a context of its own making, and
// the window a task's lock is named after has to reach the run. It is also a
// module answering hesape's contract, not the kernel's.
func (s *Scheduler) Start(ctx context.Context) {
	loop, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.stop = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)
		s.run(loop)
	}()
}

// Stop cancels the loop and waits for the run in flight.
//
// Waiting matters: a task killed halfway is a task whose lock is still held and
// whose work is half done, and the next window will not know either.
func (s *Scheduler) Stop(ctx context.Context) error {
	if s.stop == nil {
		return nil
	}
	s.stop()

	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run ticks at the top of each minute.
//
// Aligned to the minute rather than every sixty seconds from boot, because a
// task specified as "0 3 * * *" has to fire at 3:00 and not at 3:00 plus
// however long after a deploy the process happened to start.
func (s *Scheduler) run(ctx context.Context) {
	log := observability.Log(ctx).With("component", "scheduler")
	log.Info("scheduler started", "tasks", len(s.entries))

	for {
		now := s.opts.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)

		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
		}

		s.Tick(ctx, s.opts.Now())
	}
}

// Tick fires everything due in the minute of at.
//
// Exported because `aru schedule:run` and the tests drive the same code path
// the loop drives. A second entry point that "runs a task manually" would be a
// second implementation, and the manual one always ends up subtly different.
//
// What is due, and what a per-tenant task expands to, is decided by
// scheduling.Runner.
func (s *Scheduler) Tick(ctx context.Context, at time.Time) {
	runner := scheduling.NewRunner(s.schedule)
	runner.Now = s.opts.Now
	runner.Listen = s.report(ctx)

	runner.Run(withWindow(ctx, at), at)
}

// RunNow runs one task by id, outside its schedule.
//
// Same lock, same Grant, same instrumentation -- which is what makes the manual
// run auditable rather than a back door.
func (s *Scheduler) RunNow(ctx context.Context, id, tenant string) error {
	for _, e := range s.entries {
		if e.task.ID != id {
			continue
		}
		if e.task.Scope == kernel.PerTenant && tenant == "" {
			return fmt.Errorf("%s runs per tenant: say which one with --tenant", id)
		}

		// The scheduled event is shared by every tick. A manual run gets its own
		// event because the tenant and the callback result are operation state;
		// putting either on the shared event lets concurrent runs overwrite one
		// another even when their execution is otherwise serialized.
		event := scheduling.NewCallbackEvent(nil, s.work(e), nil)
		event.Name(e.task.ID)
		event.Cron(e.task.Spec)
		event.Action(e.task.Action)
		if e.task.Scope == kernel.PerTenant {
			event.PerTenant()
			event.Tenant(tenant)
		}
		return event.Run(withWindow(ctx, s.opts.Now()))
	}
	return fmt.Errorf("no task with id %s. `aru schedule:list` shows the registered ones", id)
}

// report logs what the runner refuses to run.
//
// It is the outer half of the two lines a failure used to produce: the one that
// says a task failed, next to the detailed one execute writes with the duration
// and the query count. It is also the only report of the two failures the run
// never reaches -- a per-tenant task with no resolver, and a resolver that
// failed -- which hesape hands over as a ScheduledTaskFailed.
func (s *Scheduler) report(ctx context.Context) scheduling.Listener {
	return func(event any) {
		failed, ok := event.(events.ScheduledTaskFailed)
		if !ok {
			return
		}

		log := observability.Log(ctx).With("component", "scheduler", "task", failed.Task.GetSummaryForDisplay())
		if task, ok := failed.Task.(*scheduling.Event); ok {
			if tenant := task.Grant().Subject().Tenant; tenant != "" {
				log = log.With("tenant", tenant)
			}
		}
		log.Error("task failed", "error", failed.Exception)
	}
}

// work is what the hesape event calls. The Grant is built by hesape, from the
// event's action and the tenant it was expanded for, and it is the same value
// security.SystemGrant returns: security.Grant is an alias for auth.Grant, so
// the callback the framework writes is a scheduling.Callback without conversion.
func (s *Scheduler) work(e *entry) scheduling.Callback {
	return func(ctx context.Context, g security.Grant) error {
		return s.runOne(ctx, e, g, s.window(ctx))
	}
}

// runOne executes a task under its lock.
func (s *Scheduler) runOne(ctx context.Context, e *entry, g security.Grant, at time.Time) error {
	work := func(ctx context.Context) error { return s.execute(ctx, e, g, at) }

	if !e.task.Singleton || s.opts.Locker == nil {
		return work(ctx)
	}

	// The window is in the key, so two replicas that tick a second apart still
	// contend for the same lock -- and the TTL is the timeout, so a replica
	// that dies holding it releases at the same moment the run would have been
	// abandoned anyway.
	name := fmt.Sprintf("sched:%s:%s:%d", g.Subject().Tenant, e.task.ID, at.Truncate(time.Minute).Unix())

	err := s.opts.Locker.Run(ctx, name, e.task.Timeout, work)
	if err != nil && isLocked(err) {
		// Another replica has it. That is the lock working, not a failure.
		return nil
	}
	return err
}

// isLocked recognizes "somebody else holds it" without importing whatever holds
// the lock.
//
// By message rather than by type, which is ugly and is the price of the core
// not depending on the implementation. The alternative -- a sentinel here that
// the implementation imports -- inverts the dependency the wrong way.
func isLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "lock is held")
}

// execute is the run itself: timeout, Collector, log.
func (s *Scheduler) execute(ctx context.Context, e *entry, g security.Grant, at time.Time) error {
	// The timeout is the framework's: a hesape Event has no field for one, and
	// a scheduled run with no bound is a run that holds its lock until the TTL.
	runCtx, cancel := context.WithTimeout(ctx, e.task.Timeout)
	defer cancel()

	// A Collector, so the task shows up on the debug console with its queries
	// and its timeline. "The nightly task is slow" is the same investigation as
	// "the page is slow", and it deserves the same page.
	//
	// Only when a recorder is wired: see Options.Recorder.
	id := fmt.Sprintf("%s@%d", e.task.ID, at.Unix())
	var col *observability.Collector
	if s.opts.Recorder != nil {
		col = observability.NewCollector(id)
		runCtx = observability.WithCollector(runCtx, col)
	}

	log := observability.Log(runCtx).With("component", "scheduler", "task", e.task.ID, "tenant", g.Subject().Tenant)
	runCtx = observability.WithLogger(runCtx, log)

	start := time.Now()
	err := e.task.Run(runCtx, g)
	duration := time.Since(start)

	if col != nil {
		// Method and Path name the task rather than a route, so the console
		// list reads "task billing.nightly" next to "GET /invoices".
		s.opts.Recorder.Record(observability.Recorded{
			RequestID: id,
			Method:    "task",
			Path:      e.task.ID,
			Duration:  duration,
			At:        start,
			Collector: col,
		})
	}

	e.mu.Lock()
	e.lastRun = at
	e.lastError = ""
	if err != nil {
		e.lastError = err.Error()
	}
	e.mu.Unlock()

	if err != nil {
		log.Error("task failed",
			"duration_ms", duration.Milliseconds(),
			"queries", col.QueryCount(),
			"error", err)
		return err
	}

	log.Info("task done",
		"duration_ms", duration.Milliseconds(),
		"queries", col.QueryCount(),
		"sql_ms", col.QueryTime().Milliseconds())
	return nil
}

// windowKey is how the minute being fired reaches the run.
//
// It travels in the context because the call between them is hesape's: the
// runner decides what is due and expands it, and hands the callback nothing but
// a context and a Grant. The lock is named after the window, so the window has
// to arrive.
type windowKey struct{}

// withWindow marks the context with the minute being fired.
func withWindow(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, windowKey{}, at)
}

// window reads the minute being fired, and falls back to the clock for a run
// that reached the callback some other way.
func (s *Scheduler) window(ctx context.Context) time.Time {
	if at, ok := ctx.Value(windowKey{}).(time.Time); ok {
		return at
	}
	return s.opts.Now()
}

// Registered is one task, as `aru schedule:list` prints it.
type Registered struct {
	ID        string
	Spec      string
	Scope     string
	Singleton bool
	Timeout   time.Duration
	Next      time.Time
	LastRun   time.Time
	LastError string
}

// List returns the registered tasks with their next run.
func (s *Scheduler) List() []Registered {
	now := s.opts.Now()
	out := make([]Registered, 0, len(s.entries))

	for _, e := range s.entries {
		e.mu.Lock()
		lastRun, lastError := e.lastRun, e.lastError
		e.mu.Unlock()

		scope := "global"
		if e.task.Scope == kernel.PerTenant {
			scope = "per tenant"
		}
		out = append(out, Registered{
			ID:        e.task.ID,
			Spec:      e.cron.String(),
			Scope:     scope,
			Singleton: e.task.Singleton,
			Timeout:   e.task.Timeout,
			Next:      e.cron.Next(now),
			LastRun:   lastRun,
			LastError: lastError,
		})
	}
	return out
}

// Diagnose reports tasks that are overdue or that failed.
//
// It feeds the error page through kernel.Diagnostic. A task that stopped firing
// looks exactly like a task with nothing to do, and the gap between the last run
// and the schedule is what tells them apart.
func (s *Scheduler) Diagnose(ctx context.Context) []string {
	now := s.opts.Now()
	var out []string

	for _, e := range s.entries {
		e.mu.Lock()
		lastRun, lastError := e.lastRun, e.lastError
		e.mu.Unlock()

		if lastError != "" {
			out = append(out, fmt.Sprintf("The scheduled task %s failed on its last run: %s", e.task.ID, lastError))
		}
		if lastRun.IsZero() {
			// Nothing to say: the process may have started a minute ago.
			continue
		}

		// Two windows late means something is wrong -- the loop stopped, or a
		// lock is held by a replica that died.
		expected := e.cron.Next(lastRun)
		if expected.IsZero() {
			continue
		}
		if second := e.cron.Next(expected); !second.IsZero() && now.After(second) {
			out = append(out, fmt.Sprintf(
				"The scheduled task %s last ran %s and was due at %s. Is the scheduler running?",
				e.task.ID, lastRun.Format(time.RFC3339), expected.Format(time.RFC3339)))
		}
	}
	return out
}
