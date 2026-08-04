package scheduler_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/scheduler"
	"github.com/arandu-io/framework/security"
)

func at(spec string) time.Time {
	t, err := time.Parse(time.RFC3339, spec)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseAcceptsWhatPeopleWrite(t *testing.T) {
	for _, c := range []struct {
		spec    string
		matches string
		misses  string
	}{
		{"* * * * *", "2026-08-03T13:47:00Z", ""},
		{"0 3 * * *", "2026-08-03T03:00:00Z", "2026-08-03T04:00:00Z"},
		{"30 * * * *", "2026-08-03T13:30:00Z", "2026-08-03T13:31:00Z"},
		{"*/15 * * * *", "2026-08-03T13:45:00Z", "2026-08-03T13:44:00Z"},
		{"0 9-17 * * *", "2026-08-03T09:00:00Z", "2026-08-03T18:00:00Z"},
		{"0 0 1 * *", "2026-08-01T00:00:00Z", "2026-08-02T00:00:00Z"},
		{"0 0 * * 1", "2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z"}, // a Monday
		{"0,30 * * * *", "2026-08-03T13:30:00Z", "2026-08-03T13:15:00Z"},
		{"@daily", "2026-08-03T00:00:00Z", "2026-08-03T01:00:00Z"},
		{"@hourly", "2026-08-03T13:00:00Z", "2026-08-03T13:01:00Z"},
	} {
		s, err := scheduler.Parse(c.spec)
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if !s.Matches(at(c.matches)) {
			t.Errorf("%q does not match %s", c.spec, c.matches)
		}
		if c.misses != "" && s.Matches(at(c.misses)) {
			t.Errorf("%q matches %s and should not", c.spec, c.misses)
		}
	}
}

// TestParseRefusesWhatWouldNeverFire: an unparseable spec caught at boot beats
// a task that silently never runs, which is the failure mode of every scheduler
// that validates lazily.
func TestParseRefusesWhatWouldNeverFire(t *testing.T) {
	for _, spec := range []string{
		"",            // nothing
		"* * * *",     // four fields
		"* * * * * *", // six: seconds are deliberately not supported
		"60 * * * *",  // minute out of range
		"* 24 * * *",  // hour out of range
		"* * * * 7",   // weekday out of range
		"abc * * * *", // not a number
		"5-1 * * * *", // counts backwards
		"*/0 * * * *", // a step of zero is an infinite set
		"@every 30s",  // the shorthand that would be a busy loop
	} {
		if _, err := scheduler.Parse(spec); err == nil {
			t.Errorf("%q was accepted", spec)
		}
	}
}

// TestTheErrorNamesTheField: "invalid cron" sends people to the wrong field of
// five.
func TestTheErrorNamesTheField(t *testing.T) {
	_, err := scheduler.Parse("* 99 * * *")
	if err == nil {
		t.Fatal("an hour of 99 was accepted")
	}
	if !strings.Contains(err.Error(), "hour") {
		t.Errorf("the error does not name the field: %v", err)
	}
}

func TestNextFindsTheFollowingRun(t *testing.T) {
	s := scheduler.MustParse("0 3 * * *")

	next := s.Next(at("2026-08-03T13:00:00Z"))
	if want := at("2026-08-04T03:00:00Z"); !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

// TestNextGivesUpOnASpecThatNeverFires: February 30th matches nothing, and
// returning zero is what lets `aru schedule:list` say so instead of hanging.
func TestNextGivesUpOnASpecThatNeverFires(t *testing.T) {
	s := scheduler.MustParse("0 0 30 2 *")

	if next := s.Next(at("2026-08-03T13:00:00Z")); !next.IsZero() {
		t.Fatalf("next = %s, want the zero time", next)
	}
}

// TestDayAndWeekdayAreOr matches Vixie cron: "0 0 1 * 1" is the first of the
// month AND every Monday, not their intersection. It surprises people, and
// matching the surprise beats being the one implementation that differs.
func TestDayAndWeekdayAreOr(t *testing.T) {
	s := scheduler.MustParse("0 0 1 * 1")

	if !s.Matches(at("2026-08-01T00:00:00Z")) { // the first, a Saturday
		t.Error("the first of the month did not match")
	}
	if !s.Matches(at("2026-08-03T00:00:00Z")) { // a Monday, not the first
		t.Error("a Monday did not match")
	}
	if s.Matches(at("2026-08-04T00:00:00Z")) { // a Tuesday, not the first
		t.Error("a Tuesday matched")
	}
}

// --- the scheduler itself ---

func task(id string, run func(context.Context, security.Grant) error) kernel.Task {
	return kernel.Task{
		ID: id, Spec: "* * * * *", Action: security.Action(id),
		Timeout: time.Second, Run: run,
	}
}

func TestATaskRunsUnderItsGrant(t *testing.T) {
	var got security.Grant
	tk := task("billing.close", func(_ context.Context, g security.Grant) error {
		got = g
		return nil
	})
	tk.Scope = kernel.PerTenant

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Tenants: func(context.Context) ([]string, error) { return []string{"t-1"}, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Tick(context.Background(), at("2026-08-03T13:00:00Z"))

	if got.Action() != "billing.close" || got.Subject().Tenant != "t-1" {
		t.Fatalf("the task ran under %q for %q", got.Action(), got.Subject().Tenant)
	}
	// The Grant passes for its own action and nothing else, which is what makes
	// the scheduler no different from a request.
	if err := got.Check("billing.close"); err != nil {
		t.Errorf("the Grant fails its own action: %v", err)
	}
	if err := got.Check("billing.delete"); err == nil {
		t.Error("the Grant passed for an action it was not issued for")
	}
}

// TestAGlobalTaskCannotReachTenantData is RULE 14 meeting the scheduler, and
// the answer is the strict one: SystemGrant refuses an empty tenant, so a
// Global task holds the zero Grant and cannot pass any Check.
//
// That is a constraint, not an oversight. Global work is cleaning temporary
// files, warming a cache, checking a certificate -- none of which reads a
// customer's rows. A task that needs to read them is per tenant, and saying so
// is the whole point.
func TestAGlobalTaskCannotReachTenantData(t *testing.T) {
	var got security.Grant
	tk := task("cache.warm", func(_ context.Context, g security.Grant) error {
		got = g
		return nil
	})
	// Global is the default scope, stated here because it is the subject.
	tk.Scope = kernel.Global

	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{})
	s.Tick(context.Background(), at("2026-08-03T13:00:00Z"))

	if got.Subject().Tenant != "" {
		t.Fatalf("a global task got a tenant: %q", got.Subject().Tenant)
	}
	if err := got.Check("cache.warm"); err == nil {
		t.Fatal("a global task holds a Grant that passes Check, and could read a tenant's rows")
	}
}

// TestAPerTenantTaskExpands: one lock and one Grant per tenant, which is what
// keeps a task from reading across customers.
func TestAPerTenantTaskExpands(t *testing.T) {
	var mu sync.Mutex
	var tenants []string

	tk := task("billing.close", func(_ context.Context, g security.Grant) error {
		mu.Lock()
		defer mu.Unlock()
		tenants = append(tenants, g.Subject().Tenant)
		return nil
	})
	tk.Scope = kernel.PerTenant

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Tenants: func(context.Context) ([]string, error) { return []string{"t-1", "t-2"}, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Tick(context.Background(), at("2026-08-03T13:00:00Z"))

	if len(tenants) != 2 || tenants[0] != "t-1" || tenants[1] != "t-2" {
		t.Fatalf("ran for %v, want one run per tenant", tenants)
	}
}

// TestAPerTenantTaskWithNoResolverDoesNotRunSilently: a task that never fires
// is the kind of thing found months later, and only if somebody goes looking.
func TestAPerTenantTaskWithNoResolverDoesNotRunSilently(t *testing.T) {
	ran := false
	tk := task("billing.close", func(context.Context, security.Grant) error {
		ran = true
		return nil
	})
	tk.Scope = kernel.PerTenant

	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{})
	s.Tick(context.Background(), at("2026-08-03T13:00:00Z"))

	if ran {
		t.Fatal("a per-tenant task ran with no tenant")
	}
}

// TestOnlyOneReplicaRunsASingleton is what the lock is for: with N replicas, a
// task scheduled every minute runs N times unless exactly one wins.
func TestOnlyOneReplicaRunsASingleton(t *testing.T) {
	var mu sync.Mutex
	runs := 0

	build := func(locker kernel.Locker) *scheduler.Scheduler {
		tk := task("billing.close", func(context.Context, security.Grant) error {
			mu.Lock()
			defer mu.Unlock()
			runs++
			return nil
		})
		tk.Singleton = true
		s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: locker})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s
	}

	// One lock shared by both, like one Redis shared by two pods.
	locker := &memoryLocker{held: map[string]bool{}}
	first, second := build(locker), build(locker)

	window := at("2026-08-03T13:00:00Z")
	first.Tick(context.Background(), window)
	second.Tick(context.Background(), window)

	if runs != 1 {
		t.Fatalf("the task ran %d times in one window, want 1", runs)
	}
}

// TestANonSingletonRunsEverywhere: opting out has to actually opt out, or the
// flag is decoration.
func TestANonSingletonRunsEverywhere(t *testing.T) {
	var mu sync.Mutex
	runs := 0

	locker := &memoryLocker{held: map[string]bool{}}
	build := func() *scheduler.Scheduler {
		tk := task("cache.warm", func(context.Context, security.Grant) error {
			mu.Lock()
			defer mu.Unlock()
			runs++
			return nil
		})
		s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: locker})
		return s
	}

	window := at("2026-08-03T13:00:00Z")
	build().Tick(context.Background(), window)
	build().Tick(context.Background(), window)

	if runs != 2 {
		t.Fatalf("a non-singleton ran %d times on two replicas, want 2", runs)
	}
}

// TestTheLockIsPerWindow: two replicas a second apart still contend for the
// same lock, and the next minute is a new one.
func TestTheLockIsPerWindow(t *testing.T) {
	var mu sync.Mutex
	runs := 0

	tk := task("billing.close", func(context.Context, security.Grant) error {
		mu.Lock()
		defer mu.Unlock()
		runs++
		return nil
	})
	tk.Singleton = true

	locker := &memoryLocker{held: map[string]bool{}}
	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: locker})

	s.Tick(context.Background(), at("2026-08-03T13:00:10Z"))
	s.Tick(context.Background(), at("2026-08-03T13:00:50Z")) // same minute
	s.Tick(context.Background(), at("2026-08-03T13:01:00Z")) // the next one

	if runs != 2 {
		t.Fatalf("ran %d times across two windows, want 2", runs)
	}
}

func TestNewRefusesTasksThatCannotWork(t *testing.T) {
	valid := task("ok", func(context.Context, security.Grant) error { return nil })

	for _, c := range []struct {
		name  string
		tasks []kernel.Task
	}{
		{"no id", []kernel.Task{{Spec: "* * * * *", Run: valid.Run}}},
		{"no run", []kernel.Task{{ID: "x", Spec: "* * * * *"}}},
		{"bad spec", []kernel.Task{{ID: "x", Spec: "nope", Run: valid.Run}}},
		{"duplicate id", []kernel.Task{valid, valid}},
	} {
		if _, err := scheduler.New(c.tasks, scheduler.Options{}); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
}

// TestRunNowUsesTheSamePath: a manual run that took a different route would be
// a back door, and the two would drift.
func TestRunNowUsesTheSamePath(t *testing.T) {
	var got security.Grant
	tk := task("billing.close", func(_ context.Context, g security.Grant) error {
		got = g
		return nil
	})
	tk.Spec = "0 3 * * *" // not now
	tk.Scope = kernel.PerTenant

	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{})

	if err := s.RunNow(context.Background(), "billing.close", "t-9"); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if got.Subject().Tenant != "t-9" {
		t.Errorf("ran for %q", got.Subject().Tenant)
	}

	// A per-tenant task with no tenant is a mistake worth naming.
	if err := s.RunNow(context.Background(), "billing.close", ""); err == nil {
		t.Error("a per-tenant task ran with no tenant")
	}
	if err := s.RunNow(context.Background(), "nope", ""); err == nil {
		t.Error("an unknown id ran")
	}
}

func TestListReportsTheNextRun(t *testing.T) {
	tk := task("billing.close", func(context.Context, security.Grant) error { return nil })
	tk.Spec = "0 3 * * *"
	tk.Singleton = true

	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Now: func() time.Time { return at("2026-08-03T13:00:00Z") },
	})

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("listed %d tasks", len(list))
	}
	if list[0].ID != "billing.close" || list[0].Spec != "0 3 * * *" || !list[0].Singleton {
		t.Fatalf("listed %+v", list[0])
	}
	if want := at("2026-08-04T03:00:00Z"); !list[0].Next.Equal(want) {
		t.Errorf("next = %s, want %s", list[0].Next, want)
	}
}

// TestAFailedTaskIsDiagnosed: a task that fails silently every night is the
// worst kind, because the first sign is the thing it was supposed to do never
// having happened.
func TestAFailedTaskIsDiagnosed(t *testing.T) {
	tk := task("billing.close", func(context.Context, security.Grant) error {
		return errors.New("the ledger is locked")
	})

	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{})
	if got := s.Diagnose(context.Background()); len(got) != 0 {
		t.Fatalf("a scheduler that has not run yet diagnosed %v", got)
	}

	s.Tick(context.Background(), at("2026-08-03T13:00:00Z"))

	diagnosis := s.Diagnose(context.Background())
	if len(diagnosis) == 0 {
		t.Fatal("a failed task produced no diagnosis")
	}
	if !strings.Contains(diagnosis[0], "the ledger is locked") {
		t.Errorf("the diagnosis does not say why: %q", diagnosis[0])
	}
}

// TestAnOverdueTaskIsDiagnosed: a scheduler that stopped looks exactly like one
// with nothing to do.
func TestAnOverdueTaskIsDiagnosed(t *testing.T) {
	tk := task("billing.close", func(context.Context, security.Grant) error { return nil })

	now := at("2026-08-03T13:00:00Z")
	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Now: func() time.Time { return now },
	})

	// It ran an hour ago and the schedule is every minute.
	s.Tick(context.Background(), at("2026-08-03T12:00:00Z"))

	diagnosis := s.Diagnose(context.Background())
	if len(diagnosis) == 0 {
		t.Fatal("a task an hour overdue produced no diagnosis")
	}
	if !strings.Contains(diagnosis[0], "Is the scheduler running?") {
		t.Errorf("the diagnosis does not say what to check: %q", diagnosis[0])
	}
}

func TestStartAndStop(t *testing.T) {
	tk := task("noop", func(context.Context, security.Grant) error { return nil })
	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{})

	s.Start(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stopping twice, and stopping one that never started, are both no-ops:
	// shutdown paths get called from more places than anyone expects.
	if err := s.Stop(ctx); err != nil {
		t.Errorf("the second Stop: %v", err)
	}
	fresh, _ := scheduler.New(nil, scheduler.Options{})
	if err := fresh.Stop(ctx); err != nil {
		t.Errorf("stopping a scheduler that never started: %v", err)
	}
}

// memoryLocker stands in for github.com/arandu-io/kv, which is tested against a
// real server in its own repository. This one exists to prove the scheduler
// asks for the lock and honors the answer.
type memoryLocker struct {
	mu   sync.Mutex
	held map[string]bool
}

func (l *memoryLocker) Run(ctx context.Context, name string, _ time.Duration, fn func(context.Context) error) error {
	l.mu.Lock()
	if l.held[name] {
		l.mu.Unlock()
		// The same wording the adapter uses, which is what the scheduler
		// matches on to tell "somebody else has it" from a real failure.
		return errors.New("kv: the lock is held")
	}
	l.held[name] = true
	l.mu.Unlock()

	return fn(ctx)
}

// TestTheKernelCollectsTasksFromModules: same shape as Migrations(), so a
// module declares its scheduled work the way it declares everything else.
func TestTheKernelCollectsTasksFromModules(t *testing.T) {
	k := kernel.New(config.Config{Env: config.EnvDev}).Register(&schedulingModule{})

	tasks := k.Tasks()
	if len(tasks) != 1 || tasks[0].ID != "billing.close" {
		t.Fatalf("collected %+v", tasks)
	}
}

// TestBootFailsOnAnUnparseableSpec: the application does not start with a task
// that would silently never run. That is "fail fast at boot" applied to
// schedules.
func TestBootFailsOnAnUnparseableSpec(t *testing.T) {
	m := scheduler.NewModule([]kernel.Task{{
		ID: "x", Spec: "not a cron", Run: func(context.Context, security.Grant) error { return nil },
	}}, scheduler.Options{})

	if err := m.Boot(context.Background()); err == nil {
		t.Fatal("the module booted with an unparseable spec")
	}
}

// TestAModuleWithNoTasksIsInert: registering the scheduler in an application
// that schedules nothing must not start a loop or fail.
func TestAModuleWithNoTasksIsInert(t *testing.T) {
	m := scheduler.NewModule(nil, scheduler.Options{})

	if err := m.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := m.Diagnose(context.Background()); len(got) != 0 {
		t.Errorf("diagnosed %v", got)
	}
}

// schedulingModule is a module that declares a task and nothing else.
type schedulingModule struct{}

func (*schedulingModule) Name() string         { return "billing" }
func (*schedulingModule) Routes(*httpx.Router) {}
func (*schedulingModule) Schedule() []kernel.Task {
	return []kernel.Task{{
		ID: "billing.close", Spec: "0 3 * * *", Action: "billing.close",
		Run: func(context.Context, security.Grant) error { return nil },
	}}
}
