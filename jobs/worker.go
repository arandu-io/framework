// The worker loop, answered by github.com/arandu-io/hesape/queue.
//
// Nothing here drains a queue. The loop, the batch, the backoff, the decision
// to park a job and the instrumentation all run in hesape/queue.Worker; what is
// written here is the three renames between the two sides, and the adapter that
// presents a framework Queue as a hesape one.

package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	hqueue "github.com/arandu-io/hesape/queue"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
)

// WorkerOptions configures the loop.
//
// It stays declared here rather than aliasing hesape/queue.WorkerOptions, which
// renamed Poll to Sleep and MaxAttempts to MaxTries and added nine fields this
// contract never had. The defaults are not applied here any more: every
// zero means the same thing on the other side, and hesape decides it once.
type WorkerOptions struct {
	// Queue is which queue to drain. Empty means DefaultQueue.
	Queue string
	// Concurrency is how many jobs run at once. Default 4.
	Concurrency int
	// Lease is how long a reserved job stays invisible to other workers. It has
	// to exceed the longest handler, or a second worker picks up work still in
	// progress. Default 5 minutes.
	Lease time.Duration
	// Poll is how long to wait before asking again when the queue was empty.
	// Default 1 second.
	//
	// Renamed on the way to hesape: it is WorkerOptions.Sleep there.
	Poll time.Duration
	// MaxAttempts is how many failures a job gets before it is parked.
	// Default 5.
	//
	// Renamed on the way to hesape: it is WorkerOptions.MaxTries there.
	MaxAttempts int
	// Recorder receives each finished job, so it shows on /_arandu/debug with
	// its queries and its timeline -- exactly like a request.
	//
	// Nil means no instrumentation, and that is what production looks like: no
	// Collector is built, FromContext returns nil, and every Record method is a
	// no-op on a nil receiver. Zero cost, not low cost.
	//
	// Building a Collector on every job unconditionally and then throwing it
	// away would make production pay for recording every query with its bound
	// arguments and its caller frames, with nobody able to read any of it. Pass
	// kernel.Recorder() to turn it on.
	Recorder *observability.Recorder
	// Backoff returns how long to wait before attempt n. Default is
	// exponential, capped at an hour.
	Backoff func(attempt int) time.Duration
}

// hesape is the translation: two renames, and everything else by the same name.
//
// The zero values travel as they are. hesape/queue.WorkerOptions.withDefaults
// fills in the same numbers this package documents -- concurrency 4, a five
// minute lease, a one second poll, five attempts and the exponential backoff --
// so a caller who set nothing gets what they always got.
//
// Timeout is left zero on purpose: hesape defaults it to the Lease, which is
// the deadline this contract has always given a handler.
func (o WorkerOptions) hesape() hqueue.WorkerOptions {
	return hqueue.WorkerOptions{
		Queue:       o.Queue,
		Concurrency: o.Concurrency,
		Lease:       o.Lease,
		Sleep:       o.Poll,
		MaxTries:    o.MaxAttempts,
		Recorder:    o.Recorder,
		Backoff:     o.Backoff,
	}
}

// ExponentialBackoff doubles the wait each attempt, capped at an hour.
//
// Capped, because unbounded doubling means the eleventh attempt is next year --
// and a job nobody will ever see fail is worse than one that parks.
//
// A wrapper and not an alias: a plain function has no alias form in Go.
func ExponentialBackoff(attempt int) time.Duration { return hqueue.ExponentialBackoff(attempt) }

// Worker runs jobs off a queue.
//
// In the same binary as the application, started by `aru work`, which is the
// same image with a different argument. Not a second artifact: one image is what
// keeps the deploy story in doc 17 true.
//
// It is an envelope over hesape/queue.Worker because the design diverged in
// three ways at once: the loop is called Daemon there and answers with an exit
// status a supervisor reads, a handler takes a *jobs.Job so it can settle its
// own job, and the queue underneath is asked eleven questions rather than eight.
// The three translations are here and nothing else is.
type Worker struct {
	inner *hqueue.Worker
}

// NewWorker returns the worker.
func NewWorker(q Queue, opts WorkerOptions) *Worker {
	return &Worker{inner: hqueue.NewWorker(&queueAdapter{queue: q}, opts.hesape())}
}

// Handle registers the handler for a job name.
//
// Registering twice panics rather than replacing. Two handlers for one name is
// an import nobody meant to add, and finding out at boot beats finding out from
// work that silently went to the wrong place.
func (w *Worker) Handle(name string, h Handler) *Worker {
	w.inner.Handle(name, handlerAdapter{handler: h})
	return w
}

// HandleFunc registers a function.
func (w *Worker) HandleFunc(name string, f HandlerFunc) *Worker { return w.Handle(name, f) }

// Names returns the registered job names, for `aru work` to print at start.
func (w *Worker) Names() []string { return w.inner.Names() }

// Run drains the queue until the context is cancelled.
//
// Renamed on the way to hesape: it is Worker.Daemon there, and it answers with
// the exit status a process supervisor reads. This contract has no place to put
// a status -- a cancelled context is the only way it ever ended -- so the status
// is dropped and the error is passed through.
func (w *Worker) Run(ctx context.Context) error {
	_, err := w.inner.Daemon(ctx)
	return err
}

// handlerAdapter presents a Handler as a hesape/queue.Handler.
//
// One rename of the argument: a job is a *jobs.Job there, because a handler on
// that side may release or park its own job. On this side the Queue settles it,
// so the pointer is unwrapped and the settlement is left to the worker.
type handlerAdapter struct{ handler Handler }

var _ hqueue.Handler = handlerAdapter{}

func (a handlerAdapter) Handle(ctx context.Context, g security.Grant, j *hjobs.Job) error {
	return a.handler.Handle(ctx, g, jobFrom(j))
}

// errNotInTheOldContract is what the adapter answers for the two questions
// hesape/queue.Queue asks that this package's Queue never did.
//
// Neither is reachable through this bridge: the only thing holding an adapter
// is the Worker, and a worker asks a queue to pop, release, delete and fail.
// Answering with an error rather than a wrong number is what keeps a driver
// from looking like it supports something it does not.
var errNotInTheOldContract = errors.New("jobs: the framework's Queue contract has no counterpart for this; import github.com/arandu-io/hesape/queue and use a driver written against it")

// queueAdapter presents a Queue as a hesape/queue.Queue, and as the driver
// behind every job it hands out.
//
// It is the whole of the rename between the two contracts, and the settlement
// half is the part worth reading: hesape's worker calls Release, Delete and Fail
// on the job, and each one lands on exactly the call the old worker made --
// Fail(retryAt, park=false), Ack and Fail(park=true) -- so a driver sees the
// same three statements in the same three situations it always did.
type queueAdapter struct{ queue Queue }

var (
	_ hqueue.Queue = (*queueAdapter)(nil)
	_ hjobs.Driver = (*queueAdapter)(nil)
)

// Push adds a job to the queue named on it.
func (a *queueAdapter) Push(ctx context.Context, g security.Grant, j hjobs.Job) error {
	return a.queue.Push(ctx, g, jobFrom(&j))
}

// PushOn adds a job to a named queue, ignoring the one on the job.
func (a *queueAdapter) PushOn(ctx context.Context, g security.Grant, queue string, j hjobs.Job) error {
	j.Queue = queue
	return a.Push(ctx, g, j)
}

// Later adds a job that becomes eligible after delay.
func (a *queueAdapter) Later(ctx context.Context, g security.Grant, delay time.Duration, j hjobs.Job) error {
	j.RunAt = time.Now().UTC().Add(delay)
	return a.Push(ctx, g, j)
}

// Bulk adds many jobs. One push each: the old contract has no batch, and a
// driver that can do it in one round trip is one written against hesape.
func (a *queueAdapter) Bulk(ctx context.Context, g security.Grant, js []hjobs.Job) error {
	for _, j := range js {
		if err := a.Push(ctx, g, j); err != nil {
			return err
		}
	}
	return nil
}

// Pop takes up to n jobs off a queue and hides them for the lease.
//
// Renamed on the way to hesape: it is Reserve here. Each job comes back
// attached to this adapter, which is what lets hesape's worker settle it
// through the eight-method contract underneath.
func (a *queueAdapter) Pop(ctx context.Context, queue string, n int, lease time.Duration) ([]*hjobs.Job, error) {
	reserved, err := a.queue.Reserve(ctx, queue, n, lease)
	if err != nil {
		return nil, err
	}
	out := make([]*hjobs.Job, 0, len(reserved))
	for _, j := range reserved {
		out = append(out, hjobs.Popped(a, "", j.hesape()))
	}
	return out, nil
}

// Size is how many jobs the queue holds, waiting or in flight.
//
// The old contract has no such question: Pending counts what is waiting, and a
// reserved job is running. Answering with that number would report a drained
// queue as empty while a worker still held work.
func (a *queueAdapter) Size(context.Context, string) (int, error) {
	return 0, errNotInTheOldContract
}

// PendingSize is how many jobs are waiting. Renamed on the way to hesape: it is
// Pending here.
func (a *queueAdapter) PendingSize(ctx context.Context, queue string) (int, error) {
	return a.queue.Pending(ctx, queue)
}

// CreationTimeOfOldestPendingJob is when the oldest waiting job became
// eligible, or the zero time when nothing is waiting.
//
// The old contract answers the same question the other way round -- Oldest is
// how long it has been waiting -- so this subtracts. A duration of zero means
// nothing is waiting there, including the case of a job scheduled for the
// future, and the zero time is how that is said here.
func (a *queueAdapter) CreationTimeOfOldestPendingJob(ctx context.Context, queue string) (time.Time, error) {
	waited, err := a.queue.Oldest(ctx, queue)
	if err != nil || waited <= 0 {
		return time.Time{}, err
	}
	return time.Now().Add(-waited), nil
}

// Clear removes every job on a queue. The old contract has no counterpart, and
// deleting rows one at a time through Ack is not the same operation.
func (a *queueAdapter) Clear(context.Context, string) (int, error) {
	return 0, errNotInTheOldContract
}

// Failed lists the jobs that gave up. Renamed on the way to hesape: it is
// Parked here.
func (a *queueAdapter) Failed(ctx context.Context, limit int) ([]hjobs.Job, error) {
	parked, err := a.queue.Parked(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]hjobs.Job, 0, len(parked))
	for _, j := range parked {
		out = append(out, j.hesape())
	}
	return out, nil
}

// Retry puts a failed job back in line with its attempts reset.
func (a *queueAdapter) Retry(ctx context.Context, uuid string) error {
	return a.queue.Retry(ctx, uuid)
}

// ReleaseJob puts the job back on its queue, eligible again after delay.
//
// It is the old Fail with park false, which is the statement the old worker
// made after a failure that had attempts left. The cause is read back off the
// job because hesape records it there before releasing, and the old contract
// takes it as an argument.
func (a *queueAdapter) ReleaseJob(ctx context.Context, j *hjobs.Job, delay time.Duration) error {
	var cause error
	if j.LastError != "" {
		cause = errors.New(j.LastError)
	}
	return a.queue.Fail(ctx, jobFrom(j), cause, time.Now().Add(delay), false)
}

// DeleteJob removes a finished job. It is the old Ack.
func (a *queueAdapter) DeleteJob(ctx context.Context, j *hjobs.Job) error {
	return a.queue.Ack(ctx, jobFrom(j))
}

// FailJob parks the job. It is the old Fail with park true, and a zero retryAt
// because a parked job has no next delivery to schedule.
func (a *queueAdapter) FailJob(ctx context.Context, j *hjobs.Job, cause error) error {
	return a.queue.Fail(ctx, jobFrom(j), cause, time.Time{}, true)
}
