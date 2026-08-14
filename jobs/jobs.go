// The job, its constructor and the two Grant functions, answered by
// github.com/arandu-io/hesape/queue/jobs.
//
// The constant and the errors alias. Job, Handler and Queue stay declared here
// and translate, for the three reasons in the package doc.

package jobs

import (
	"context"
	"time"

	"github.com/arandu-io/framework/security"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
)

// DefaultQueue is where a job goes when nobody said otherwise.
const DefaultQueue = hjobs.DefaultQueue

// Job is one unit of work waiting to run.
//
// It stays declared here rather than aliasing hesape/queue/jobs.Job, which
// renamed ID to UUID, added three fields and put a driver behind the job so
// it can settle itself. Three things outside this module read this shape:
// github.com/arandu-io/queue writes j.ID into a column, its kv sibling marshals
// this struct to JSON and back, and `aru make:job` generates against it. An
// alias would rename a column and a wire field at once.
//
// The three fields hesape added -- DisplayName, Exceptions and Attributes --
// and the settlement bookkeeping behind them have no counterpart here, and
// nothing on this side ever sets them, so a job that crosses the bridge and
// comes back is the job that went in.
type Job struct {
	// ID is the deduplication key. It is stable across retries, which is what
	// makes a handler able to recognize work it already did.
	//
	// Renamed on the way to hesape: it is jobs.Job.UUID there, because it
	// answers both uuid() and getJobId() of Illuminate's job contract.
	ID string
	// Queue separates work by urgency: a password reset email and a monthly
	// report should not wait behind each other.
	Queue string
	// Name routes the job to its handler: "invoice.send", "report.monthly".
	Name string
	// TenantID is who the work belongs to. It comes from the Grant at Push, and
	// the worker rebuilds a Grant from it -- a job with no tenant cannot be
	// scoped, and everything downstream of it reads across customers.
	TenantID string
	// Payload is the arguments, as JSON. Keep it to facts and ids: a payload
	// that says "look it up" is a payload that reads a row which has already
	// changed.
	Payload []byte
	// AuthorizedBy and Action record the Grant that pushed it, which is the
	// audit trail and what the worker reissues the work under.
	AuthorizedBy string
	Action       string
	// RunAt is when it becomes eligible. Zero means now.
	RunAt time.Time
	// Attempts counts the deliveries INCLUDING the current one: a job being
	// handled for the first time has Attempts == 1. LastError is why the most
	// recent one failed -- stored rather than logged, because the thing anyone
	// needs at 3am is "this failed twelve times with this message".
	Attempts  int
	LastError string
}

// hesape is half the translation this bridge exists to do. The value it returns
// carries no driver, so it can be pushed and cannot settle itself -- which is
// what a Job on this side has always been.
func (j Job) hesape() hjobs.Job {
	return hjobs.Job{
		UUID:         j.ID,
		Queue:        j.Queue,
		Name:         j.Name,
		TenantID:     j.TenantID,
		Payload:      j.Payload,
		AuthorizedBy: j.AuthorizedBy,
		Action:       j.Action,
		RunAt:        j.RunAt,
		Attempts:     j.Attempts,
		LastError:    j.LastError,
	}
}

// jobFrom is the other half. It takes a pointer because that is what a job off
// a hesape queue is, and it drops the settlement bookkeeping deliberately: on
// this side the Queue settles the job, so there is nothing here to carry it.
func jobFrom(h *hjobs.Job) Job {
	return Job{
		ID:           h.UUID,
		Queue:        h.Queue,
		Name:         h.Name,
		TenantID:     h.TenantID,
		Payload:      h.Payload,
		AuthorizedBy: h.AuthorizedBy,
		Action:       h.Action,
		RunAt:        h.RunAt,
		Attempts:     h.Attempts,
		LastError:    h.LastError,
	}
}

// Decode unmarshals the payload into v.
//
// It calls through rather than unmarshalling here, so the one description of
// what a malformed payload reads like is the one in hesape.
func (j Job) Decode(v any) error {
	h := j.hesape()
	return h.Decode(v)
}

// Handler does the work.
//
// The Grant is rebuilt from the job's tenant and action, so a handler reaches
// repositories the same way a service does. There is no unauthorized path into
// the database from a worker, which is the whole point of the Grant existing.
//
// It stays declared here rather than aliasing hesape/queue.Handler, which takes
// a *jobs.Job so the handler can release or park its own job. Every handler in
// a project is written against this signature -- `aru make:job` emits it -- and
// an alias would ask all of them to be rewritten, which is the opposite of what
// a bridge is for.
type Handler interface {
	Handle(ctx context.Context, g security.Grant, j Job) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, g security.Grant, j Job) error

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, g security.Grant, j Job) error {
	return f(ctx, g, j)
}

// Queue is what a driver implements.
//
// Reserve/Ack/Fail rather than a channel of jobs: the job has to stay in the
// store, invisible to other workers, until it is acknowledged. A worker that
// dies mid-job must not lose it, and that is not something a channel can offer.
//
// It stays declared here, with the old eight method names, rather than aliasing
// hesape/queue.Queue -- which has eleven and renamed all but two: Reserve is
// Pop, Ack is delegated to the job itself, Fail is split across Release and
// Fail on the job, Parked is Failed, Pending is PendingSize and Oldest is
// CreationTimeOfOldestPendingJob, answering with a time rather than a duration.
//
// github.com/arandu-io/queue and github.com/arandu-io/queue/kv implement this
// interface, by these names, and they are separate modules: an alias here would
// compile in the framework and break both drivers silently, which is the one
// failure this bridge exists to prevent. queueAdapter, in worker.go, is what
// carries an implementation of this across to hesape.
type Queue interface {
	// Push adds a job. The tenant comes from the Grant.
	Push(ctx context.Context, g security.Grant, j Job) error
	// Reserve takes up to n jobs off a queue and hides them for the lease.
	// Jobs whose lease expires become visible again -- which is what makes a
	// worker crash recoverable and delivery at-least-once.
	//
	// The returned jobs carry Attempts INCLUDING this delivery: a job handed
	// over for the first time has Attempts == 1. A driver that returns the
	// count from before the delivery makes the worker park a job one attempt
	// early, and with MaxAttempts of 2 it parks on the first failure and never
	// retries at all.
	Reserve(ctx context.Context, queue string, n int, lease time.Duration) ([]Job, error)
	// Ack removes a finished job.
	Ack(ctx context.Context, j Job) error
	// Fail records a failure and schedules the retry, or parks the job when it
	// has had enough attempts.
	Fail(ctx context.Context, j Job, cause error, retryAt time.Time, park bool) error
	// Parked lists the jobs that gave up, so they can be inspected and retried.
	Parked(ctx context.Context, limit int) ([]Job, error)
	// Retry puts a parked job back in line with its attempts reset.
	Retry(ctx context.Context, id string) error
	// Pending is how many jobs are waiting on a queue. It feeds the health
	// check: a queue that only grows is a worker that is not running.
	Pending(ctx context.Context, queue string) (int, error)
	// Oldest is how long the oldest waiting job has been waiting. A stopped
	// worker looks exactly like an idle one, and this is what tells them apart.
	Oldest(ctx context.Context, queue string) (time.Duration, error)
}

// ErrNoTenant is returned when a Grant carries no tenant.
//
// It is an error rather than a default, and that is RULE 14 with teeth: a job
// with no tenant cannot be scoped, and everything the handler touches would read
// across customers.
//
// The alias is what keeps errors.Is correct across the two modules: a driver
// that checks this value and a hesape function that returns jobs.ErrNoTenant
// are looking at one error.
var ErrNoTenant = hjobs.ErrNoTenant

// ErrNoName is returned when a job has no name to route by.
var ErrNoName = hjobs.ErrNoName

// ErrForged is returned when a job claims an action or a tenant the Grant
// pushing it does not carry.
var ErrForged = hjobs.ErrForged

// New builds a job from a Grant and a payload.
//
// This is the only constructor, so every job in the system carries a tenant, an
// id and the Grant that authorized it -- there is no shape of Job that skipped
// any of the three.
//
// A wrapper and not an alias: a plain function has no alias form in Go, and the
// job it answers with has to cross back over the rename.
func New(g security.Grant, queue, name string, payload any) (Job, error) {
	h, err := hjobs.New(g, queue, name, payload)
	if err != nil {
		return Job{}, err
	}
	return jobFrom(&h), nil
}

// GrantFor rebuilds the Grant a job runs under.
//
// The action and the tenant come from the row, so the worker reissues exactly
// what the push authorized -- not more. A worker that invented its own Grant
// would be a way to reach the database with permissions nobody granted.
//
// It is load-bearing for RULE 14 and it is one line: hesape mints the Grant, so
// there is no second definition of what a job is allowed to do.
func GrantFor(j Job) security.Grant {
	h := j.hesape()
	return hjobs.GrantFor(&h)
}

// Authorized reports whether a job may be pushed under this Grant.
//
// Every driver calls it at the top of Push, and it closes an escalation the
// contract otherwise allows. New builds a job from the Grant, so what it
// produces always matches -- but Push takes a Job, and a Job is a struct
// anybody can fill in:
//
//	j := jobs.Job{ID: id, Name: "invoice.send", Action: "invoice.delete", TenantID: other}
//	queue.Push(ctx, viewGrant, j)
//
// The worker rebuilds the Grant from the row -- GrantFor gives
// SystemGrant(j.Action, j.TenantID) -- so the handler would run with an action
// nobody authorized, in a tenant nobody authorized, and every Policy
// downstream would say yes because the Grant looks legitimate. The queue would
// be the one way past the authorization the whole framework exists to enforce.
// Found by audit.
//
// The decision is hesape's, so the two drivers in this collection and any
// written against hesape/queue directly refuse the same job.
func Authorized(g security.Grant, j Job) error {
	return hjobs.Authorized(g, j.hesape())
}
