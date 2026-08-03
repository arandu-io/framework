// Package jobs is the queue contract: work that happens after the response.
//
// The contract lives in the core and the drivers do not, for the same reason as
// data.Repository: Push takes a security.Grant, and the tenant comes from it.
// Moving that into an optional package would make the guarantee optional, and an
// optional guarantee is not one.
//
// A driver is a separate module under github.com/arandu-io/queue, because in Go
// there is no optional dependency and a core that carried a Redis client would
// put it in every project's go.sum.
//
// Delivery is at-least-once. A handler that cannot run twice safely is a handler
// with a bug -- the process can die between doing the work and acknowledging it,
// and no queue anywhere solves that.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// DefaultQueue is where a job goes when nobody said otherwise.
const DefaultQueue = "default"

// Job is one unit of work waiting to run.
type Job struct {
	// ID is the deduplication key. It is stable across retries, which is what
	// makes a handler able to recognize work it already did.
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
	// Attempts counts the deliveries so far, and LastError is why the most
	// recent one failed. Stored rather than logged, because the thing anyone
	// needs at 3am is "this failed twelve times with this message".
	Attempts  int
	LastError string
}

// Decode unmarshals the payload into v.
func (j Job) Decode(v any) error {
	if err := json.Unmarshal(j.Payload, v); err != nil {
		return fmt.Errorf("jobs: decoding %s: %w", j.Name, err)
	}
	return nil
}

// Handler does the work.
//
// The Grant is rebuilt from the job's tenant and action, so a handler reaches
// repositories the same way a service does. There is no unauthorized path into
// the database from a worker, which is the whole point of the Grant existing.
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
type Queue interface {
	// Push adds a job. The tenant comes from the Grant.
	Push(ctx context.Context, g security.Grant, j Job) error
	// Reserve takes up to n jobs off a queue and hides them for the lease.
	// Jobs whose lease expires become visible again -- which is what makes a
	// worker crash recoverable and delivery at-least-once.
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
var ErrNoTenant = errors.New("jobs: the Grant carries no tenant, and a job without one cannot be scoped")

// ErrNoName is returned when a job has no name to route by.
var ErrNoName = errors.New("jobs: a job with no name cannot be routed to a handler")

// New builds a job from a Grant and a payload.
//
// This is the only constructor, so every job in the system carries a tenant, an
// id and the Grant that authorized it -- there is no shape of Job that skipped
// any of the three.
func New(g security.Grant, queue, name string, payload any) (Job, error) {
	if name == "" {
		return Job{}, ErrNoName
	}
	tenant := data.Tenant(g)
	if tenant == "" {
		return Job{}, ErrNoTenant
	}
	if queue == "" {
		queue = DefaultQueue
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Job{}, fmt.Errorf("jobs: serializing %s: %w", name, err)
	}
	id, err := data.NewID()
	if err != nil {
		return Job{}, err
	}

	return Job{
		ID:           id,
		Queue:        queue,
		Name:         name,
		TenantID:     tenant,
		Payload:      body,
		AuthorizedBy: g.Subject().ID,
		Action:       string(g.Action()),
	}, nil
}

// GrantFor rebuilds the Grant a job runs under.
//
// The action and the tenant come from the row, so the worker reissues exactly
// what the push authorized -- not more. A worker that invented its own Grant
// would be a way to reach the database with permissions nobody granted.
func GrantFor(j Job) security.Grant {
	return security.SystemGrant(security.Action(j.Action), j.TenantID)
}
