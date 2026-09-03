// What the bridge is tested for: that the old name reaches the new behaviour.
//
// The cron expression itself is tested in
// github.com/arandu-io/hesape/console/scheduling, against the code that now
// parses it, and so are the Grant a callback receives and the expansion of a
// per-tenant event. What is here is the crossing: that a kernel.Task becomes an
// event hesape fires, that the window reaches the lock, and that the two renamed
// cron methods still answer.

package unit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/scheduler"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/console/scheduling"
)

// Tenants is an alias, so a resolver written against either name satisfies both.
// The assignment in both directions is the whole proof.
var (
	_ scheduler.Tenants  = scheduling.Tenants(nil)
	_ scheduling.Tenants = scheduler.Tenants(nil)
)

func at(spec string) time.Time {
	t, err := time.Parse(time.RFC3339, spec)
	if err != nil {
		panic(err)
	}
	return t
}

// TestTheCronEnvelopeAnswersWithTheHesapeExpression: Matches is IsDue and Next
// is GetNextRunDate, and nothing in this package decides either.
func TestTheCronEnvelopeAnswersWithTheHesapeExpression(t *testing.T) {
	for _, spec := range []string{"* * * * *", "0 3 * * *", "*/15 * * * *", "0 0 1 * 1", "@daily"} {
		bridged, err := scheduler.Parse(spec)
		if err != nil {
			t.Errorf("%q: %v", spec, err)
			continue
		}
		direct := scheduling.MustParseCronExpression(spec)

		if bridged.String() != direct.String() {
			t.Errorf("%q: String = %q, hesape says %q", spec, bridged.String(), direct.String())
		}
		for _, when := range []string{
			"2026-08-03T00:00:00Z", "2026-08-03T03:00:00Z",
			"2026-08-03T13:45:00Z", "2026-08-04T00:00:00Z",
		} {
			if bridged.Matches(at(when)) != direct.IsDue(at(when)) {
				t.Errorf("%q at %s: Matches disagrees with IsDue", spec, when)
			}
		}
		if got, want := bridged.Next(at("2026-08-03T13:00:00Z")), direct.GetNextRunDate(at("2026-08-03T13:00:00Z")); !got.Equal(want) {
			t.Errorf("%q: Next = %s, GetNextRunDate = %s", spec, got, want)
		}
	}
}

// TestAnUnparseableSpecIsStillRefusedByTheOldNames: the error crosses the
// bridge, and MustParse keeps the prefix of the package the caller wrote
// against.
func TestAnUnparseableSpecIsStillRefusedByTheOldNames(t *testing.T) {
	if _, err := scheduler.Parse("@every 30s"); err == nil {
		t.Fatal("the shorthand that would be a busy loop was accepted")
	}

	defer func() {
		recovered, ok := recover().(string)
		if !ok {
			t.Fatal("MustParse did not panic on an unparseable spec")
		}
		if !strings.HasPrefix(recovered, "scheduler: ") {
			t.Errorf("the panic reads %q, and names another package", recovered)
		}
	}()
	scheduler.MustParse("not a cron")
}

// --- the scheduler itself ---

func task(id string, run func(context.Context, security.Grant) error) kernel.Task {
	return kernel.Task{
		ID: id, Spec: "* * * * *", Action: security.Action(id),
		Timeout: time.Second, Run: run,
	}
}

// TestATaskRunsUnderItsGrant: the Grant is built by hesape now, from the event's
// action and the tenant it was expanded for, and it has to be the same value the
// task used to receive.
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

// TestAGlobalTaskCannotReachTenantData is the tenant rule meeting the
// scheduler, and the answer is the strict one: SystemGrant refuses an empty
// tenant, so a Global task holds the zero Grant and cannot pass any Check.
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
// keeps a task from reading across customers. The expansion is hesape's; that
// Options.Tenants still drives it is the bridge.
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
//
// The lock stays on this side of the bridge -- hesape claims a window through a
// SchedulingMutex, which marks and never releases, and kernel.Locker wraps the
// run -- so this is the test of the thing that did not move.
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

// TestALateReplicaCannotRepeatASingletonOccurrence reproduces the production
// gap with the real framework adapter. The second replica arrives only after
// the first task has finished, so a transient lock has already been released;
// the occurrence claim still has to reject it for the same UTC minute.
func TestALateReplicaCannotRepeatASingletonOccurrence(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))

	build := func() *scheduler.Scheduler {
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

	window := at("2026-08-03T13:00:00Z")
	build().Tick(context.Background(), window)
	build().Tick(context.Background(), window)

	if runs != 1 {
		t.Fatalf("a late replica ran the same occurrence %d times, want 1", runs)
	}
}

// TestConcurrentReplicasClaimOneSingletonOccurrence starts every replica at
// the same barrier. The store's atomic acquisition, surfaced through the typed
// outcome, must select exactly one winner under the race detector.
func TestConcurrentReplicasClaimOneSingletonOccurrence(t *testing.T) {
	const replicas = 32
	window := at("2026-08-03T13:00:00Z")
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))

	var runsMu sync.Mutex
	runs := 0
	schedulers := make([]*scheduler.Scheduler, replicas)
	for i := range schedulers {
		tk := task("billing.close", func(context.Context, security.Grant) error {
			runsMu.Lock()
			runs++
			runsMu.Unlock()
			return nil
		})
		tk.Singleton = true

		var err error
		schedulers[i], err = scheduler.New([]kernel.Task{tk}, scheduler.Options{
			Locker: locker,
			Now:    func() time.Time { return window },
		})
		if err != nil {
			t.Fatalf("New replica %d: %v", i, err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, replicas)
	var ready sync.WaitGroup
	ready.Add(replicas)
	for _, s := range schedulers {
		go func() {
			ready.Done()
			<-start
			errs <- s.RunNow(context.Background(), "billing.close", "")
		}()
	}
	ready.Wait()
	close(start)

	for range replicas {
		if err := <-errs; err != nil {
			t.Errorf("RunNow: %v", err)
		}
	}
	runsMu.Lock()
	defer runsMu.Unlock()
	if runs != 1 {
		t.Fatalf("%d concurrent replicas ran the occurrence, want 1", runs)
	}
}

// TestReplicasInSeparateProcessesRunOneOccurrence is the one test here whose
// claim is about a server rather than about this code.
//
// Every other proof of the claim runs against a store inside this process,
// where the winner is decided by a mutex this repository owns. Production
// decides it in Redis, and the shape of the decision is different there: the
// claim survives because SET NX PX writes the key and its expiry in one
// command, and the losers learn they lost from a null reply rather than from a
// map lookup. A correction can pass against every in-process store and still
// be wrong about that, so this one spawns real processes -- separate address
// spaces, separate schedulers, one server between them -- and counts the runs
// in the server.
//
// Without REDIS_ADDRESS it skips, and skipping is the honest answer: a proof
// about a server that quietly passes without one proves nothing.
func TestReplicasInSeparateProcessesRunOneOccurrence(t *testing.T) {
	addr := os.Getenv("REDIS_ADDRESS")
	if addr == "" {
		t.Skip("REDIS_ADDRESS is not set: start a RESP server and set it, e.g. REDIS_ADDRESS=127.0.0.1:6379")
	}

	// A child re-enters this same function. It is the parent's own binary, so
	// the scheduler it builds is the one under test and not a copy of it.
	if prefix := os.Getenv(replicaPrefixEnv); prefix != "" {
		runOneReplicaProcess(t, addr, prefix)
		return
	}

	prefix := fmt.Sprintf("arandu-test:%s:%d:%d:", t.Name(), os.Getpid(), time.Now().UnixNano())

	server, err := dialRESP(addr, prefix)
	if err != nil {
		t.Fatalf("connecting to %s: %v", addr, err)
	}
	// Registered before the cleanup that needs the connection, because cleanups
	// run in reverse: a deferred Close here would run first and leave every
	// delete below talking to a closed socket.
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() {
		// The claim is deliberately never released, so the keys this run wrote
		// would otherwise sit in somebody's development server for an hour.
		// The name is spelled out rather than read back, because reading it
		// back would mean asking the server which keys exist and believing the
		// answer -- and this test is the one that does not do that.
		claim := fmt.Sprintf("lock:sched::%s:%d", replicaTaskID, at(replicaWindow).Unix())
		if _, _, err := server.do("DEL", prefix+"runs", prefix+claim); err != nil {
			t.Errorf("clearing the keys this run wrote: %v", err)
		}
	})

	replica := func(i int) {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(), replicaPrefixEnv+"="+prefix)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("replica %d: %v\n%s", i, err, out)
		}
	}

	// Two waves, because they fail differently and only one of them is the
	// defect this test was written for.
	//
	// The first wave asks at once, and a lock held for the length of the run
	// already answers it: the losers are refused while the winner is still
	// working. The second wave starts after every process in the first has
	// exited, so a lock released when its function returned is gone and the
	// key is free -- which is how the same occurrence used to be executed
	// twice, and why one wave alone would have proved nothing.
	const together = 6
	var wave sync.WaitGroup
	for i := range together {
		wave.Add(1)
		go func() {
			defer wave.Done()
			replica(i)
		}()
	}
	wave.Wait()

	for i := range 2 {
		replica(together + i)
	}

	ran, ok, err := server.do("GET", prefix+"runs")
	if err != nil {
		t.Fatalf("counting the runs: %v", err)
	}
	if !ok {
		t.Fatal("no replica ran the occurrence at all, so nothing was proved about running it once")
	}
	if ran != "1" {
		t.Fatalf("%s processes ran the same occurrence, want 1", ran)
	}
}

// runOneReplicaProcess is the child half: one replica, claiming the occurrence
// every other replica is claiming, counting its run in the server so the
// parent can read a number no child could have inflated on its own.
func runOneReplicaProcess(t *testing.T, addr, prefix string) {
	t.Helper()

	server, err := dialRESP(addr, prefix)
	if err != nil {
		t.Fatalf("connecting to %s: %v", addr, err)
	}
	defer func() { _ = server.Close() }()

	tk := task(replicaTaskID, func(context.Context, security.Grant) error {
		if _, _, err := server.do("INCR", prefix+"runs"); err != nil {
			return err
		}
		return nil
	})
	tk.Singleton = true

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Locker: foundation.NewLocker(cache.NewLocks(server)),
		Now:    func() time.Time { return at(replicaWindow) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.RunNow(context.Background(), tk.ID, ""); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
}

// TestOccurrenceIdentitySeparatesTasksAndTenants proves the explicit identity
// tuple. Task ID and tenant select the occurrence; Action and captured
// parameters must be represented by a distinct task ID when they select
// distinct work.
func TestOccurrenceIdentitySeparatesTasksAndTenants(t *testing.T) {
	window := at("2026-08-03T13:00:00Z")
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))
	var mu sync.Mutex
	runs := map[string]int{}

	build := func(id string) kernel.Task {
		tk := task(id, func(_ context.Context, g security.Grant) error {
			mu.Lock()
			defer mu.Unlock()
			runs[id+"/"+g.Subject().Tenant]++
			return nil
		})
		tk.Scope = kernel.PerTenant
		tk.Singleton = true
		return tk
	}

	s, err := scheduler.New(
		[]kernel.Task{build("billing.close"), build("billing.remind")},
		scheduler.Options{
			Locker:  locker,
			Now:     func() time.Time { return window },
			Tenants: func(context.Context) ([]string, error) { return []string{"tenant-1", "tenant-2"}, nil },
		},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s.Tick(context.Background(), window)
	s.Tick(context.Background(), window)

	mu.Lock()
	defer mu.Unlock()
	for _, identity := range []string{
		"billing.close/tenant-1",
		"billing.close/tenant-2",
		"billing.remind/tenant-1",
		"billing.remind/tenant-2",
	} {
		if runs[identity] != 1 {
			t.Errorf("%s ran %d times, want 1", identity, runs[identity])
		}
	}
	if len(runs) != 4 {
		t.Errorf("ran unexpected identities: %v", runs)
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
//
// It is also what proves the window survives the crossing. The runner calls the
// callback with a context and a Grant and nothing else, so the minute being
// fired travels in the context; if it did not arrive, every window would share
// one lock and this would run once.
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

// TestAdjacentSingletonWindowsRemainIndependent keeps occurrence deduplication
// separate from overlap prevention. A later task may opt into an overlap
// window; Singleton alone continues to let two distinct UTC minutes run at the
// same time.
func TestAdjacentSingletonWindowsRemainIndependent(t *testing.T) {
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))
	firstWindow := at("2026-08-03T13:00:00Z")
	secondWindow := at("2026-08-03T13:01:00Z")
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondRan := make(chan struct{})

	firstTask := task("billing.close", func(context.Context, security.Grant) error {
		close(firstStarted)
		<-releaseFirst
		return nil
	})
	firstTask.Singleton = true
	secondTask := task("billing.close", func(context.Context, security.Grant) error {
		close(secondRan)
		return nil
	})
	secondTask.Singleton = true

	first, err := scheduler.New([]kernel.Task{firstTask}, scheduler.Options{
		Locker: locker,
		Now:    func() time.Time { return firstWindow },
	})
	if err != nil {
		t.Fatalf("New first: %v", err)
	}
	second, err := scheduler.New([]kernel.Task{secondTask}, scheduler.Options{
		Locker: locker,
		Now:    func() time.Time { return secondWindow },
	})
	if err != nil {
		t.Fatalf("New second: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- first.RunNow(context.Background(), firstTask.ID, "") }()
	<-firstStarted
	if err := second.RunNow(context.Background(), secondTask.ID, ""); err != nil {
		t.Fatalf("second window: %v", err)
	}
	select {
	case <-secondRan:
	default:
		t.Fatal("the adjacent window did not run while the first was in flight")
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first window: %v", err)
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

// TestNewRefusesATransientOnlyLockerForSingletonTasks prevents a custom Locker
// from silently advertising a guarantee it cannot provide. Non-singleton work
// remains compatible with the original Run-only contract.
func TestNewRefusesATransientOnlyLockerForSingletonTasks(t *testing.T) {
	tk := task("billing.close", func(context.Context, security.Grant) error { return nil })
	tk.Singleton = true

	_, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: transientLocker{}})
	want := "scheduler: Singleton tasks require a Locker with durable occurrence claims"
	if err == nil || err.Error() != want {
		t.Fatalf("New error = %v, want %q", err, want)
	}

	tk.Singleton = false
	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: transientLocker{}})
	if err != nil {
		t.Fatalf("New non-singleton: %v", err)
	}
	if err := s.RunNow(context.Background(), tk.ID, ""); err != nil {
		t.Fatalf("RunNow non-singleton: %v", err)
	}
}

// TestNilLockerPreservesSingleReplicaSemantics keeps the documented opt-out.
// With no shared locker, each scheduler trusts that it is the only replica and
// runs its local Singleton task.
func TestNilLockerPreservesSingleReplicaSemantics(t *testing.T) {
	window := at("2026-08-03T13:00:00Z")
	var mu sync.Mutex
	runs := 0
	build := func() *scheduler.Scheduler {
		tk := task("billing.close", func(context.Context, security.Grant) error {
			mu.Lock()
			runs++
			mu.Unlock()
			return nil
		})
		tk.Singleton = true
		s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s
	}

	build().Tick(context.Background(), window)
	build().Tick(context.Background(), window)

	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Fatalf("two single-replica schedulers ran %d times, want 2", runs)
	}
}

// TestDuplicateRegistrationIsDeterministicUnderConcurrency makes the
// construction-time registry race itself. Every builder must reject the same
// identity with the same error, and construction never executes the task.
func TestDuplicateRegistrationIsDeterministicUnderConcurrency(t *testing.T) {
	const builders = 64
	var runsMu sync.Mutex
	runs := 0
	duplicate := task("billing.close", func(context.Context, security.Grant) error {
		runsMu.Lock()
		runs++
		runsMu.Unlock()
		return nil
	})
	want := `scheduler: two tasks share the id "billing.close", and the lock cannot tell them apart`

	start := make(chan struct{})
	errs := make(chan error, builders)
	var ready sync.WaitGroup
	ready.Add(builders)
	for range builders {
		go func() {
			ready.Done()
			<-start
			_, err := scheduler.New([]kernel.Task{duplicate, duplicate}, scheduler.Options{})
			errs <- err
		}()
	}
	ready.Wait()
	close(start)

	for range builders {
		if err := <-errs; err == nil || err.Error() != want {
			t.Errorf("New error = %v, want %q", err, want)
		}
	}
	runsMu.Lock()
	defer runsMu.Unlock()
	if runs != 0 {
		t.Fatalf("duplicate registration executed the task %d times", runs)
	}
}

// TestAnUnknownOccurrenceOutcomeFailsClosed reserves the outcome zero value.
// A custom implementation that forgets to set its result must never authorize
// the task accidentally.
func TestAnUnknownOccurrenceOutcomeFailsClosed(t *testing.T) {
	ran := false
	tk := task("billing.close", func(context.Context, security.Grant) error {
		ran = true
		return nil
	})
	tk.Singleton = true
	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: fixedOutcomeLocker{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = s.RunNow(context.Background(), tk.ID, "")
	want := "scheduler: occurrence claimer returned invalid outcome 0"
	if err == nil || err.Error() != want {
		t.Fatalf("RunNow error = %v, want %q", err, want)
	}
	if ran {
		t.Fatal("the task ran under an invalid occurrence outcome")
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

// TestConcurrentRunNowKeepsEachTenantOnItsOwnOperation pauses the first call
// after it has selected a tenant, lets the second finish, and then resumes it.
// The ordering is explicit so the semantic leak is reproducible even when the
// race detector has nothing to report.
func TestConcurrentRunNowKeepsEachTenantOnItsOwnOperation(t *testing.T) {
	firstAtClock := make(chan struct{})
	releaseFirst := make(chan struct{})
	var clockMu sync.Mutex
	clockCalls := 0
	now := func() time.Time {
		clockMu.Lock()
		clockCalls++
		call := clockCalls
		clockMu.Unlock()
		if call == 1 {
			close(firstAtClock)
			<-releaseFirst
		}
		return at("2026-08-03T13:00:00Z")
	}

	tenants := make(chan string, 2)
	tk := task("billing.close", func(_ context.Context, g security.Grant) error {
		tenants <- g.Subject().Tenant
		return nil
	})
	tk.Scope = kernel.PerTenant

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{Now: now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- s.RunNow(context.Background(), "billing.close", "tenant-first")
	}()
	<-firstAtClock

	secondErr := s.RunNow(context.Background(), "billing.close", "tenant-second")
	secondTenant := <-tenants
	close(releaseFirst)
	firstErr := <-firstDone
	firstTenant := <-tenants

	if secondErr != nil {
		t.Errorf("RunNow for second tenant: %v", secondErr)
	}
	if firstErr != nil {
		t.Errorf("RunNow for first tenant: %v", firstErr)
	}
	if secondTenant != "tenant-second" {
		t.Errorf("second operation ran for %q, want tenant-second", secondTenant)
	}
	if firstTenant != "tenant-first" {
		t.Errorf("first operation ran for %q, want tenant-first", firstTenant)
	}
}

// TestRunNowReportsWhatTheTaskFailedWith: the error has to come back out of
// hesape's Event.Run, or `aru schedule:run` reports success on a task that
// failed.
func TestRunNowReportsWhatTheTaskFailedWith(t *testing.T) {
	tk := task("billing.close", func(context.Context, security.Grant) error {
		return errors.New("the ledger is locked")
	})

	s, _ := scheduler.New([]kernel.Task{tk}, scheduler.Options{})

	err := s.RunNow(context.Background(), "billing.close", "")
	if err == nil || !strings.Contains(err.Error(), "the ledger is locked") {
		t.Fatalf("RunNow reported %v", err)
	}
}

// TestAFailedSingletonKeepsItsClaimAndError proves that claim state and task
// state travel on different channels. The reserved wording is ordinary domain
// text now: the first caller receives it intact, and a late replica skips the
// already-claimed occurrence without executing it again.
func TestAFailedSingletonKeepsItsClaimAndError(t *testing.T) {
	window := at("2026-08-03T13:00:00Z")
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))
	boom := errors.New("database lock is held during reconciliation")
	runs := 0
	tk := task("billing.close", func(context.Context, security.Grant) error {
		runs++
		return boom
	})
	tk.Singleton = true

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Locker: locker,
		Now:    func() time.Time { return window },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.RunNow(context.Background(), tk.ID, ""); !errors.Is(err, boom) {
		t.Fatalf("first RunNow error = %v, want %v", err, boom)
	}
	if err := s.RunNow(context.Background(), tk.ID, ""); err != nil {
		t.Fatalf("late RunNow: %v", err)
	}
	if runs != 1 {
		t.Fatalf("failed occurrence ran %d times, want 1", runs)
	}
}

// TestACancelledSingletonKeepsItsClaim waits until the task owns the claim,
// cancels that run, then sends a late replica to the same minute. Cancellation
// propagates from the owner and does not reopen the occurrence.
func TestACancelledSingletonKeepsItsClaim(t *testing.T) {
	window := at("2026-08-03T13:00:00Z")
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))
	started := make(chan struct{})
	runs := 0
	tk := task("billing.close", func(ctx context.Context, _ security.Grant) error {
		runs++
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	tk.Singleton = true

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Locker: locker,
		Now:    func() time.Time { return window },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- s.RunNow(ctx, tk.ID, "") }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled RunNow error = %v", err)
	}

	if err := s.RunNow(context.Background(), tk.ID, ""); err != nil {
		t.Fatalf("late RunNow: %v", err)
	}
	if runs != 1 {
		t.Fatalf("cancelled occurrence ran %d times, want 1", runs)
	}
}

// TestATimedOutSingletonKeepsItsClaim proves the scheduler's own deadline is a
// task failure, not a reason to reopen the occurrence for a late replica.
func TestATimedOutSingletonKeepsItsClaim(t *testing.T) {
	window := at("2026-08-03T13:00:00Z")
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))
	runs := 0
	tk := task("billing.close", func(ctx context.Context, _ security.Grant) error {
		runs++
		<-ctx.Done()
		return ctx.Err()
	})
	tk.Singleton = true
	tk.Timeout = 20 * time.Millisecond

	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Locker: locker,
		Now:    func() time.Time { return window },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.RunNow(context.Background(), tk.ID, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out RunNow error = %v", err)
	}
	if err := s.RunNow(context.Background(), tk.ID, ""); err != nil {
		t.Fatalf("late RunNow: %v", err)
	}
	if runs != 1 {
		t.Fatalf("timed-out occurrence ran %d times, want 1", runs)
	}
}

// TestAClaimStoreErrorPropagatesByType makes the former reserved phrase come
// from the claimer rather than the task. It is still a failure unless the
// typed outcome says the occurrence was already claimed.
func TestAClaimStoreErrorPropagatesByType(t *testing.T) {
	boom := errors.New("lock is held because the database is unavailable")
	ran := false
	tk := task("billing.close", func(context.Context, security.Grant) error {
		ran = true
		return nil
	})
	tk.Singleton = true
	s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{
		Locker: fixedOutcomeLocker{err: boom},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.RunNow(context.Background(), tk.ID, ""); !errors.Is(err, boom) {
		t.Fatalf("RunNow error = %v, want %v", err, boom)
	}
	if ran {
		t.Fatal("the task ran after its occurrence claim failed")
	}
}

// TestOccurrenceClaimTTLOutlivesShortTasks keeps a late replica from entering
// the same minute after a fast task returns. Long task timeouts remain the lower
// bound so a live execution cannot outlast its own claim.
func TestOccurrenceClaimTTLIsAtLeastOneHourAndTheTaskTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "short task", timeout: time.Second, want: time.Hour},
		{name: "long task", timeout: 2 * time.Hour, want: 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			locker := &memoryLocker{held: map[string]bool{}}
			tk := task("billing.close", func(context.Context, security.Grant) error { return nil })
			tk.Singleton = true
			tk.Timeout = tc.timeout
			s, err := scheduler.New([]kernel.Task{tk}, scheduler.Options{Locker: locker})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if err := s.RunNow(context.Background(), tk.ID, ""); err != nil {
				t.Fatalf("RunNow: %v", err)
			}
			attempts := locker.claimAttempts()
			if len(attempts) != 1 {
				t.Fatalf("claim attempts = %d, want 1", len(attempts))
			}
			if attempts[0].ttl != tc.want {
				t.Fatalf("claim ttl = %s, want %s", attempts[0].ttl, tc.want)
			}
		})
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

// memoryLocker stands in for whatever an application wires in, which is tested
// against a real server wherever it is written. This one exists to prove the
// scheduler asks for the lock and honors the answer.
type memoryLocker struct {
	mu     sync.Mutex
	held   map[string]bool
	claims []claimAttempt
}

type claimAttempt struct {
	name string
	ttl  time.Duration
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

	defer func() {
		l.mu.Lock()
		delete(l.held, name)
		l.mu.Unlock()
	}()
	return fn(ctx)
}

func (l *memoryLocker) ClaimOccurrence(_ context.Context, name string, ttl time.Duration) (foundation.OccurrenceClaimOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.claims = append(l.claims, claimAttempt{name: name, ttl: ttl})

	if l.held[name] {
		return foundation.OccurrenceAlreadyClaimed, nil
	}
	l.held[name] = true
	return foundation.OccurrenceClaimAcquired, nil
}

func (l *memoryLocker) claimAttempts() []claimAttempt {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]claimAttempt(nil), l.claims...)
}

type transientLocker struct{}

func (transientLocker) Run(ctx context.Context, _ string, _ time.Duration, fn func(context.Context) error) error {
	return fn(ctx)
}

type fixedOutcomeLocker struct {
	outcome foundation.OccurrenceClaimOutcome
	err     error
}

func (l fixedOutcomeLocker) Run(ctx context.Context, _ string, _ time.Duration, fn func(context.Context) error) error {
	return fn(ctx)
}

func (l fixedOutcomeLocker) ClaimOccurrence(context.Context, string, time.Duration) (foundation.OccurrenceClaimOutcome, error) {
	return l.outcome, l.err
}

const (
	// replicaPrefixEnv carries the key namespace from the parent process to
	// its children, and its presence is also how a process knows it is a
	// child. One variable rather than two, so a child can never be started
	// against a namespace the parent will not clean up.
	replicaPrefixEnv = "ARANDU_SCHEDULER_REPLICA_PREFIX"
	replicaTaskID    = "billing.close"
	replicaWindow    = "2026-08-03T13:00:00Z"
)

// resp is the smallest RESP client that can answer cache.Locking.
//
// Written here rather than taken from a driver for two reasons. The framework
// depends on one thing it did not write, and a Redis client is not going to be
// the second. And what this proves is a claim about the server -- that SET NX
// PX refuses the second caller -- so putting a driver between the test and that
// command would add a layer the proof would then also be about.
//
// One connection behind one mutex. A lock issuer is called from several
// goroutines, and a RESP connection carries one reply at a time.
type resp struct {
	mu     sync.Mutex
	prefix string
	conn   net.Conn
	reader *bufio.Reader
}

// dialRESP opens the connection and proves it is a server that answers.
//
// The prefix namespaces every key to one run, so two runs of this test -- or a
// run against a server somebody else is using -- cannot see each other's
// claims.
func dialRESP(addr, prefix string) (*resp, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	s := &resp{prefix: prefix, conn: conn, reader: bufio.NewReader(conn)}
	if _, _, err := s.do("PING"); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *resp) Close() error { return s.conn.Close() }

// do sends one command and reads one reply.
//
// The second return says whether the server answered with a value at all. A
// null bulk string is how SET NX reports that somebody else holds the key, and
// that is an answer rather than a failure -- which is the same distinction the
// claim outcome makes one layer up.
func (s *resp) do(args ...string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out strings.Builder
	fmt.Fprintf(&out, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&out, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if err := s.conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return "", false, err
	}
	if _, err := io.WriteString(s.conn, out.String()); err != nil {
		return "", false, err
	}

	line, err := s.reader.ReadString('\n')
	if err != nil {
		return "", false, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", false, errors.New("resp: the server sent an empty reply")
	}

	switch line[0] {
	case '+', ':':
		return line[1:], true, nil
	case '-':
		return "", false, errors.New("resp: " + line[1:])
	case '$':
		size, err := strconv.Atoi(line[1:])
		if err != nil {
			return "", false, err
		}
		if size < 0 {
			return "", false, nil
		}
		body := make([]byte, size+len("\r\n"))
		if _, err := io.ReadFull(s.reader, body); err != nil {
			return "", false, err
		}
		return string(body[:size]), true, nil
	default:
		return "", false, fmt.Errorf("resp: unexpected reply %q", line)
	}
}

// AcquireLock is SET key token NX PX ttl, and it is the single command the
// whole proof rests on: the key and its expiry are written together or not at
// all, so a process that dies between them is not a case that exists.
func (s *resp) AcquireLock(_ context.Context, key, token string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, cache.ErrNoTTL
	}
	_, ok, err := s.do("SET", s.prefix+key, token, "NX", "PX", strconv.FormatInt(ttl.Milliseconds(), 10))
	return ok, err
}

// ReleaseLock deletes the key only if this token still holds it.
//
// Nothing in this proof calls it -- a claim's only way out is its expiry, which
// is the property under test -- so the gap between reading the token and
// deleting the key is not a race anything here can lose. A store meant for
// Locker.Run would have to close it, and would do it the way hesape's Redis
// store does, in a transaction.
func (s *resp) ReleaseLock(_ context.Context, key, token string) error {
	held, ok, err := s.do("GET", s.prefix+key)
	if err != nil || !ok || held != token {
		return err
	}
	_, _, err = s.do("DEL", s.prefix+key)
	return err
}

// TestTheKernelCollectsTasksFromModules: same shape as Migrations(), so a
// module declares its scheduled work the way it declares everything else.
func TestTheKernelCollectsTasksFromModules(t *testing.T) {
	cfg := bootstrap.Configuration{App: config.App{Env: config.EnvDev}}
	k := kernel.New(cfg).Register(&schedulingModule{})

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

func (*schedulingModule) Name() string        { return "billing" }
func (*schedulingModule) Routes(*http.Router) {}
func (*schedulingModule) Schedule() []kernel.Task {
	return []kernel.Task{{
		ID: "billing.close", Spec: "0 3 * * *", Action: "billing.close",
		Run: func(context.Context, security.Grant) error { return nil },
	}}
}
