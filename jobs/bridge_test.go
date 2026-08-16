// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/hesape, against the
// code that now runs; the unit tests that used to live here were tests of an
// implementation this package no longer holds -- the loop, the batch, the
// backoff, the parking -- and keeping a second copy of them would be a second
// place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- and one round trip per
// envelope, because an envelope is the place a rename can be wired to the wrong
// method and still compile. This bridge is nearly all envelope, so nearly all of
// this file is round trips.

package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/jobs"
	"github.com/arandu-io/framework/security"
	hqueue "github.com/arandu-io/hesape/queue"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
)

const tenant = "11111111-1111-4111-8111-111111111111"

func grant() security.Grant { return security.SystemGrant("invoice.send", tenant) }

// TestAliasesAreTheHesapeSymbols is the whole of the alias half of this bridge.
//
// The three errors matter more here than anywhere else: github.com/arandu-io/queue
// and its kv sibling return these values from Push, and hesape's own drivers
// return the jobs ones. The alias is what makes errors.Is answer yes across the
// two modules.
func TestAliasesAreTheHesapeSymbols(t *testing.T) {
	if jobs.DefaultQueue != hjobs.DefaultQueue {
		t.Errorf("jobs.DefaultQueue is %q, hesape says %q", jobs.DefaultQueue, hjobs.DefaultQueue)
	}

	for name, pair := range map[string][2]error{
		"ErrNoTenant": {jobs.ErrNoTenant, hjobs.ErrNoTenant},
		"ErrNoName":   {jobs.ErrNoName, hjobs.ErrNoName},
		"ErrForged":   {jobs.ErrForged, hjobs.ErrForged},
	} {
		if pair[0] != pair[1] {
			t.Errorf("jobs.%s is not the hesape value: errors.Is against it answers no", name)
		}
	}

	// A wrapper rather than an alias, so it is a thing that can be written to
	// call the wrong function.
	for _, attempt := range []int{0, 1, 5, 12, 99} {
		if got, want := jobs.ExponentialBackoff(attempt), hqueue.ExponentialBackoff(attempt); got != want {
			t.Errorf("ExponentialBackoff(%d) is %s, hesape says %s", attempt, got, want)
		}
	}
}

// TestNewReachesHesape covers the constructor, which is where the tenant is
// enforced: it comes off the Grant, and a job that has none is refused.
func TestNewReachesHesape(t *testing.T) {
	j, err := jobs.New(grant(), "", "invoice.send", map[string]string{"id": "i-1"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// The rename is the whole point: hesape mints the id into UUID and this
	// package answers with ID, and a bridge that dropped it would hand every
	// driver a job with an empty primary key.
	if j.ID == "" {
		t.Error("the job has no id: the UUID/ID rename did not carry")
	}
	if j.Queue != jobs.DefaultQueue {
		t.Errorf("the queue is %q, want the default", j.Queue)
	}
	if j.TenantID != tenant {
		t.Errorf("the tenant is %q, want %q", j.TenantID, tenant)
	}
	if j.Action != "invoice.send" {
		t.Errorf("the action is %q, want the one the Grant authorizes", j.Action)
	}

	var got map[string]string
	if err := j.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != "i-1" {
		t.Errorf("the payload came back as %v", got)
	}

	if _, err := jobs.New(security.SystemGrant("invoice.send", ""), "", "invoice.send", nil); !errors.Is(err, jobs.ErrNoTenant) {
		t.Errorf("a Grant with no tenant was not refused with ErrNoTenant: %v", err)
	}
	if _, err := jobs.New(grant(), "", "", nil); !errors.Is(err, jobs.ErrNoName) {
		t.Errorf("a job with no name was not refused with ErrNoName: %v", err)
	}
}

// TestGrantForReachesHesape covers the Grant constructor a worker reissues work
// under: the tenant and the action come off the row and nowhere else.
func TestGrantForReachesHesape(t *testing.T) {
	g := jobs.GrantFor(jobs.Job{Action: "invoice.send", TenantID: tenant})

	if err := g.Check("invoice.send"); err != nil {
		t.Fatalf("the grant does not check against the action on the job: %v", err)
	}
	if got := g.Subject().Tenant; got != tenant {
		t.Errorf("the grant is for tenant %q, want %q", got, tenant)
	}
	// A tenant that cannot be used as a namespace produces a Grant that
	// authorizes nothing, which is hesape's decision and not a second copy of it
	// here.
	if err := jobs.GrantFor(jobs.Job{Action: "invoice.send", TenantID: "../etc"}).Check("invoice.send"); err == nil {
		t.Error("a job carrying an unusable tenant produced a working Grant")
	}
}

// TestAuthorizedRefusesAForgedJob is the escalation the contract otherwise
// allows: Push takes a Job, and a Job is a struct anybody can fill in. The
// decision is hesape's, and this proves the old name still asks for it.
func TestAuthorizedRefusesAForgedJob(t *testing.T) {
	good, err := jobs.New(grant(), "", "invoice.send", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Authorized(grant(), good); err != nil {
		t.Fatalf("a job built from the Grant was refused: %v", err)
	}

	forged := good
	forged.Action = "invoice.delete"
	if err := jobs.Authorized(grant(), forged); !errors.Is(err, jobs.ErrForged) {
		t.Errorf("a job claiming an action the Grant does not carry was accepted: %v", err)
	}

	crossTenant := good
	crossTenant.TenantID = "22222222-2222-4222-8222-222222222222"
	if err := jobs.Authorized(grant(), crossTenant); !errors.Is(err, jobs.ErrForged) {
		t.Errorf("a job claiming another tenant was accepted: %v", err)
	}

	if err := jobs.Authorized(security.SystemGrant("invoice.send", ""), good); !errors.Is(err, jobs.ErrNoTenant) {
		t.Errorf("a Grant with no tenant was accepted: %v", err)
	}
}

// fakeQueue implements the framework's Queue contract, by the framework's
// method names, which is the point: it stands where github.com/arandu-io/queue
// stands, in a module the framework's build cannot check. If queueAdapter is
// wired to the wrong method, this is where it shows.
type fakeQueue struct {
	mu       sync.Mutex
	ready    []jobs.Job
	acked    []jobs.Job
	failures []failure
}

type failure struct {
	job     jobs.Job
	cause   error
	retryAt time.Time
	park    bool
}

func (q *fakeQueue) Push(_ context.Context, _ security.Grant, j jobs.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ready = append(q.ready, j)
	return nil
}

func (q *fakeQueue) Reserve(_ context.Context, _ string, n int, _ time.Duration) ([]jobs.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ready) == 0 {
		return nil, nil
	}
	if n > len(q.ready) {
		n = len(q.ready)
	}
	out := make([]jobs.Job, 0, n)
	for _, j := range q.ready[:n] {
		// Like the real drivers: the delivery is counted when the job is
		// handed over, so Attempts includes this one.
		j.Attempts++
		out = append(out, j)
	}
	q.ready = q.ready[n:]
	return out, nil
}

func (q *fakeQueue) Ack(_ context.Context, j jobs.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.acked = append(q.acked, j)
	return nil
}

func (q *fakeQueue) Fail(_ context.Context, j jobs.Job, cause error, retryAt time.Time, park bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.failures = append(q.failures, failure{job: j, cause: cause, retryAt: retryAt, park: park})
	return nil
}

func (q *fakeQueue) Parked(context.Context, int) ([]jobs.Job, error) { return nil, nil }
func (q *fakeQueue) Retry(context.Context, string) error             { return nil }
func (q *fakeQueue) Pending(context.Context, string) (int, error)    { return 0, nil }
func (q *fakeQueue) Oldest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (q *fakeQueue) state() ([]jobs.Job, []failure) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]jobs.Job(nil), q.acked...), append([]failure(nil), q.failures...)
}

func waitFor(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(message)
}

// TestTheWorkerRoundTripsAJobAndAcknowledges is the envelope's round trip:
// Reserve on this side becomes Pop on the other, the handler is called with a
// Job and not a *jobs.Job, and a finished job settles through Delete, which is
// the old Ack.
//
// Every field is checked because the two structs are field-by-field
// translations of each other and a forgotten line loses one silently -- and the
// one that would hurt most, ID, is the only field that was renamed.
func TestTheWorkerRoundTripsAJobAndAcknowledges(t *testing.T) {
	runAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	pushed := jobs.Job{
		ID:           "j-1",
		Queue:        jobs.DefaultQueue,
		Name:         "invoice.send",
		TenantID:     tenant,
		Payload:      []byte(`{"id":"i-1"}`),
		AuthorizedBy: "u-1",
		Action:       "invoice.send",
		RunAt:        runAt,
		LastError:    "the previous delivery failed",
	}

	q := &fakeQueue{}
	if err := q.Push(context.Background(), grant(), pushed); err != nil {
		t.Fatal(err)
	}

	var (
		got      jobs.Job
		gotGrant security.Grant
		done     = make(chan struct{})
	)

	w := jobs.NewWorker(q, jobs.WorkerOptions{Poll: time.Millisecond})
	w.HandleFunc("invoice.send", func(_ context.Context, g security.Grant, j jobs.Job) error {
		got, gotGrant = j, g
		close(done)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	defer cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never ran")
	}

	want := pushed
	// Reserve counts the delivery, and the worker has to see the number this
	// delivery is.
	want.Attempts = 1
	if snapshot(got) != snapshot(want) {
		t.Errorf("the job came back as\n%+v\nwant\n%+v", snapshot(got), snapshot(want))
	}

	// The handler receives a Grant for the tenant that pushed it, which is what
	// lets it reach a repository at all.
	if gotGrant.Subject().Tenant != tenant {
		t.Errorf("the handler got a Grant for %q, want %q", gotGrant.Subject().Tenant, tenant)
	}
	if err := gotGrant.Check("invoice.send"); err != nil {
		t.Errorf("the handler's Grant does not check against the job's action: %v", err)
	}

	waitFor(t, func() bool {
		acked, _ := q.state()
		return len(acked) == 1 && acked[0].ID == "j-1"
	}, "the finished job did not reach Ack: hesape's Delete is not wired to it")
}

// jobSnapshot is a Job with its payload as a string, so two of them compare
// with ==. Every exported field is on it deliberately: the translation is one
// line per field, and a line somebody forgets to write is a field that reads
// back as the zero value in the driver rather than in this build.
type jobSnapshot struct {
	ID, Queue, Name, TenantID       string
	Payload                         string
	AuthorizedBy, Action, LastError string
	RunAt                           time.Time
	Attempts                        int
}

func snapshot(j jobs.Job) jobSnapshot {
	return jobSnapshot{
		ID: j.ID, Queue: j.Queue, Name: j.Name, TenantID: j.TenantID,
		Payload:      string(j.Payload),
		AuthorizedBy: j.AuthorizedBy, Action: j.Action, LastError: j.LastError,
		RunAt: j.RunAt, Attempts: j.Attempts,
	}
}

// TestMaxAttemptsIsTheRenameOfMaxTries: hesape calls it MaxTries, and a bridge
// that dropped the rename would silently give every job hesape's default of
// five rather than the two this worker was configured with -- which is a job
// retried three times more than the application asked for, every time.
//
// It is also the round trip of the settlement half: a failure with attempts
// left is a Release on the hesape side and lands on Fail(park=false) here, with
// the cause and a retry in the future; the last one is a Fail on that side and
// lands on Fail(park=true) here.
func TestMaxAttemptsIsTheRenameOfMaxTries(t *testing.T) {
	q := &fakeQueue{}
	j, err := jobs.New(grant(), "", "invoice.send", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Push(context.Background(), grant(), j); err != nil {
		t.Fatal(err)
	}

	w := jobs.NewWorker(q, jobs.WorkerOptions{Poll: time.Millisecond, MaxAttempts: 2})
	w.HandleFunc("invoice.send", func(context.Context, security.Grant, jobs.Job) error {
		return errors.New("the invoice has no address")
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	defer cancel()

	waitFor(t, func() bool {
		_, failures := q.state()
		return len(failures) == 1
	}, "the failure was not recorded")

	_, failures := q.state()
	if failures[0].park {
		t.Error("the first failure parked the job instead of scheduling a retry")
	}
	if failures[0].cause == nil || failures[0].cause.Error() != "the invoice has no address" {
		t.Errorf("the reason was not passed to the driver: %v", failures[0].cause)
	}
	if !failures[0].retryAt.After(time.Now()) {
		t.Errorf("the retry was scheduled for %s, which is not in the future", failures[0].retryAt)
	}

	// The second delivery reaches MaxAttempts and parks.
	failed := j
	failed.Attempts = 1
	if err := q.Push(context.Background(), grant(), failed); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		_, failures := q.state()
		return len(failures) == 2 && failures[1].park
	}, "the job was not parked after the last attempt: MaxAttempts did not reach MaxTries")
}

// TestAJobWithNoHandlerParksImmediately: no amount of retrying registers a
// handler. It is here because parking without a handler is the one settlement
// hesape reaches through Fail rather than through the failure path.
func TestAJobWithNoHandlerParksImmediately(t *testing.T) {
	q := &fakeQueue{}
	j, _ := jobs.New(grant(), "", "report.monthly", nil)
	if err := q.Push(context.Background(), grant(), j); err != nil {
		t.Fatal(err)
	}

	w := jobs.NewWorker(q, jobs.WorkerOptions{Poll: time.Millisecond})
	w.HandleFunc("invoice.send", func(context.Context, security.Grant, jobs.Job) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	defer cancel()

	waitFor(t, func() bool {
		_, failures := q.state()
		return len(failures) == 1 && failures[0].park
	}, "a job with no handler was not parked")
}

// TestRegisteringTwicePanics: two handlers for one name is an import nobody
// meant to add, and the panic is hesape's. The envelope forwards the
// registration, so it has to forward the panic with it.
func TestRegisteringTwicePanics(t *testing.T) {
	w := jobs.NewWorker(&fakeQueue{}, jobs.WorkerOptions{})
	w.HandleFunc("invoice.send", func(context.Context, security.Grant, jobs.Job) error { return nil })

	defer func() {
		if recover() == nil {
			t.Error("registering a second handler for one name did not panic")
		}
	}()
	w.HandleFunc("invoice.send", func(context.Context, security.Grant, jobs.Job) error { return nil })
}

// TestNamesReachHesape: `aru work` refuses to start when nothing is registered,
// and it asks this.
func TestNamesReachHesape(t *testing.T) {
	w := jobs.NewWorker(&fakeQueue{}, jobs.WorkerOptions{})
	if len(w.Names()) != 0 {
		t.Fatalf("a worker with no handlers named %v", w.Names())
	}

	w.HandleFunc("invoice.send", func(context.Context, security.Grant, jobs.Job) error { return nil })
	names := w.Names()
	if len(names) != 1 || names[0] != "invoice.send" {
		t.Errorf("the registered names came back as %v", names)
	}
}

// TestRunStopsOnCancel is the Run/Daemon rename. Shutdown has to be clean, or a
// deploy leaves workers holding leases nobody will release -- and the exit
// status hesape answers with has to be dropped rather than turned into an error.
func TestRunStopsOnCancel(t *testing.T) {
	w := jobs.NewWorker(&fakeQueue{}, jobs.WorkerOptions{Poll: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(ctx) }()

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not stop when the context was cancelled")
	}
}
