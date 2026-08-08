// Package scheduler runs the tasks modules declare.
//
// A scheduler built on a system cron exists because the runtime has no resident
// process: cron calls a command every minute and the command decides what to
// run. That is two artifacts and a dependency on the operating system.
//
// Go has a resident process. The scheduler is a goroutine in the same binary,
// which is also what keeps the deploy story of doc 17 true: one image, no
// crontab to configure, nothing to forget when a machine is replaced.
//
// What it does not do is retry. A task that fails is logged and diagnosed, and
// the next window runs it again; work that needs its own retry budget enqueues a
// job, and the queue owns the retry. Scheduler fires, queue persists.
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
)

// Tenants returns the tenants a PerTenant task expands to.
//
// Injected, because the core does not know where the application keeps its
// tenants -- a table, a config file, a control plane. Returning an empty list
// is valid and means the task simply does not run.
type Tenants func(ctx context.Context) ([]string, error)

// Options configures the scheduler.
type Options struct {
	// Locker makes a Singleton task run on exactly one replica. Nil means a
	// single replica, and with more than one it means every replica runs
	// everything.
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
	// It used to build one on every run and throw it away, so production paid
	// for recording and the console the doc promised never showed a task. Found
	// by audit. Pass kernel.Recorder() to turn it on.
	Recorder *observability.Recorder
}

// entry is one task with its parsed schedule.
type entry struct {
	task     kernel.Task
	schedule Schedule
	// lastRun and lastError are what Diagnose and `aru schedule:list` report.
	mu        sync.Mutex
	lastRun   time.Time
	lastError string
}

// Scheduler fires tasks on their schedule.
type Scheduler struct {
	entries []*entry
	opts    Options
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
		schedule, err := Parse(t.Spec)
		if err != nil {
			return nil, fmt.Errorf("scheduler: %s: %w", t.ID, err)
		}
		if t.Timeout <= 0 {
			t.Timeout = 5 * time.Minute
		}
		entries = append(entries, &entry{task: t, schedule: schedule})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].task.ID < entries[j].task.ID })
	return &Scheduler{entries: entries, opts: opts}, nil
}

// Start runs the loop until Stop.
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
func (s *Scheduler) Tick(ctx context.Context, at time.Time) {
	for _, e := range s.entries {
		if !e.schedule.Matches(at) {
			continue
		}
		s.fire(ctx, e, at)
	}
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
		if e.task.Scope == kernel.PerTenant {
			return s.runOne(ctx, e, tenant, s.opts.Now())
		}
		return s.runOne(ctx, e, "", s.opts.Now())
	}
	return fmt.Errorf("no task with id %s. `aru schedule:list` shows the registered ones", id)
}

// fire expands the scope and runs.
func (s *Scheduler) fire(ctx context.Context, e *entry, at time.Time) {
	log := observability.Log(ctx).With("component", "scheduler", "task", e.task.ID)

	if e.task.Scope == kernel.Global {
		if err := s.runOne(ctx, e, "", at); err != nil {
			log.Error("task failed", "error", err)
		}
		return
	}

	if s.opts.Tenants == nil {
		// Reported rather than skipped in silence: a per-tenant task with no
		// resolver never runs, and that is the kind of thing found months later.
		log.Error("this task runs per tenant and no tenant resolver was wired; it will never run")
		return
	}

	tenants, err := s.opts.Tenants(ctx)
	if err != nil {
		log.Error("listing the tenants failed", "error", err)
		return
	}
	for _, tenant := range tenants {
		if err := s.runOne(ctx, e, tenant, at); err != nil {
			log.Error("task failed", "tenant", tenant, "error", err)
		}
	}
}

// runOne executes a task under its lock, its Grant and its own Collector.
func (s *Scheduler) runOne(ctx context.Context, e *entry, tenant string, at time.Time) error {
	work := func(ctx context.Context) error { return s.execute(ctx, e, tenant, at) }

	if !e.task.Singleton || s.opts.Locker == nil {
		return work(ctx)
	}

	// The window is in the key, so two replicas that tick a second apart still
	// contend for the same lock -- and the TTL is the timeout, so a replica
	// that dies holding it releases at the same moment the run would have been
	// abandoned anyway.
	name := fmt.Sprintf("sched:%s:%s:%d", tenant, e.task.ID, at.Truncate(time.Minute).Unix())

	err := s.opts.Locker.Run(ctx, name, e.task.Timeout, work)
	if err != nil && isLocked(err) {
		// Another replica has it. That is the lock working, not a failure.
		return nil
	}
	return err
}

// isLocked recognizes "somebody else holds it" without importing the adapter.
//
// By message rather than by type, which is ugly and is the price of the core
// not depending on github.com/arandu-io/kv. The alternative -- a sentinel here
// that the adapter imports -- inverts the dependency the wrong way.
func isLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "lock is held")
}

// execute is the run itself: Grant, Collector, timeout, log.
func (s *Scheduler) execute(ctx context.Context, e *entry, tenant string, at time.Time) error {
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

	log := observability.Log(runCtx).With("component", "scheduler", "task", e.task.ID, "tenant", tenant)
	runCtx = observability.WithLogger(runCtx, log)

	// The Grant is built from the task's action and the tenant, so a task
	// reaches repositories the same way a service does. There is no
	// unauthorized path into the database from the scheduler either.
	g := security.SystemGrant(e.task.Action, tenant)

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
			Spec:      e.schedule.String(),
			Scope:     scope,
			Singleton: e.task.Singleton,
			Timeout:   e.task.Timeout,
			Next:      e.schedule.Next(now),
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
		expected := e.schedule.Next(lastRun)
		if expected.IsZero() {
			continue
		}
		if second := e.schedule.Next(expected); !second.IsZero() && now.After(second) {
			out = append(out, fmt.Sprintf(
				"The scheduled task %s last ran %s and was due at %s. Is the scheduler running?",
				e.task.ID, lastRun.Format(time.RFC3339), expected.Format(time.RFC3339)))
		}
	}
	return out
}
