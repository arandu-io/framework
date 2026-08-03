package jobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/arandu-io/framework/observability"
)

// WorkerOptions configures the loop.
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
	Poll time.Duration
	// MaxAttempts is how many failures a job gets before it is parked.
	// Default 5.
	MaxAttempts int
	// Backoff returns how long to wait before attempt n. Default is
	// exponential, capped at an hour.
	Backoff func(attempt int) time.Duration
}

func (o WorkerOptions) withDefaults() WorkerOptions {
	if o.Queue == "" {
		o.Queue = DefaultQueue
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.Lease <= 0 {
		o.Lease = 5 * time.Minute
	}
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.Backoff == nil {
		o.Backoff = ExponentialBackoff
	}
	return o
}

// ExponentialBackoff doubles the wait each attempt, capped at an hour.
//
// Capped, because unbounded doubling means the eleventh attempt is next year --
// and a job nobody will ever see fail is worse than one that parks.
func ExponentialBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 12 {
		return time.Hour
	}
	wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	if wait > time.Hour {
		return time.Hour
	}
	return wait
}

// Worker runs jobs off a queue.
//
// In the same binary as the application, started by `aru work`, which is the
// same image with a different argument. Not a second artifact: one image is what
// keeps the deploy story in doc 17 true.
type Worker struct {
	queue    Queue
	handlers map[string]Handler
	opts     WorkerOptions
}

// NewWorker returns the worker.
func NewWorker(q Queue, opts WorkerOptions) *Worker {
	return &Worker{queue: q, handlers: map[string]Handler{}, opts: opts.withDefaults()}
}

// Handle registers the handler for a job name.
//
// Registering twice panics rather than replacing. Two handlers for one name is
// an import nobody meant to add, and finding out at boot beats finding out from
// work that silently went to the wrong place.
func (w *Worker) Handle(name string, h Handler) *Worker {
	if _, taken := w.handlers[name]; taken {
		panic("jobs: " + name + " already has a handler")
	}
	w.handlers[name] = h
	return w
}

// HandleFunc registers a function.
func (w *Worker) HandleFunc(name string, f HandlerFunc) *Worker { return w.Handle(name, f) }

// Names returns the registered job names, for `aru work` to print at start.
func (w *Worker) Names() []string {
	out := make([]string, 0, len(w.handlers))
	for name := range w.handlers {
		out = append(out, name)
	}
	return out
}

// Run drains the queue until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	log := observability.Log(ctx).With("component", "worker", "queue", w.opts.Queue)
	log.Info("worker started", "concurrency", w.opts.Concurrency, "handlers", len(w.handlers))

	for {
		if ctx.Err() != nil {
			return nil
		}

		reserved, err := w.queue.Reserve(ctx, w.opts.Queue, w.opts.Concurrency, w.opts.Lease)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			// A failed reserve is not fatal: the store blinked, and the next
			// poll tries again. Stopping the worker would turn a hiccup into a
			// backlog nobody is draining.
			log.Warn("reserving jobs failed", "error", err)
			if !w.wait(ctx) {
				return nil
			}
			continue
		}

		if len(reserved) == 0 {
			if !w.wait(ctx) {
				return nil
			}
			continue
		}

		// Sequential within a batch rather than one goroutine per job: the
		// batch is already sized by Concurrency, and running them in parallel
		// here would multiply that by itself.
		for _, j := range reserved {
			if ctx.Err() != nil {
				return nil
			}
			w.runOne(ctx, j)
		}
	}
}

func (w *Worker) wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(w.opts.Poll):
		return true
	}
}

// runOne executes a job with its own Collector, so it shows up on the console
// with its queries and its timeline -- exactly like a request.
//
// That is the point of instrumenting it: "the nightly job is slow" is the same
// investigation as "the page is slow", and it deserves the same page.
func (w *Worker) runOne(ctx context.Context, j Job) {
	handler, known := w.handlers[j.Name]
	if !known {
		// An unknown name is not a failure to retry: no amount of retrying will
		// register the handler. It parks immediately, where it can be seen.
		err := fmt.Errorf("no handler registered for %s", j.Name)
		observability.Log(ctx).Error("job has no handler", "job", j.Name, "id", j.ID)
		_ = w.queue.Fail(ctx, j, err, time.Time{}, true)
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, w.opts.Lease)
	defer cancel()

	col := observability.NewCollector(j.ID)
	jobCtx = observability.WithCollector(jobCtx, col)
	log := observability.Log(jobCtx).With("job", j.Name, "job_id", j.ID, "tenant", j.TenantID)
	jobCtx = observability.WithLogger(jobCtx, log)

	start := time.Now()
	err := handler.Handle(jobCtx, GrantFor(j), j)
	duration := time.Since(start)

	if err != nil {
		attempts := j.Attempts + 1
		park := attempts >= w.opts.MaxAttempts
		retryAt := time.Now().Add(w.opts.Backoff(attempts))

		if failErr := w.queue.Fail(ctx, j, err, retryAt, park); failErr != nil {
			log.Error("recording the failure failed", "error", failErr)
		}
		if park {
			log.Error("job parked after repeated failures", "attempts", attempts, "error", err)
		} else {
			log.Warn("job failed", "attempt", attempts, "retry_in", w.opts.Backoff(attempts), "error", err)
		}
		return
	}

	if err := w.queue.Ack(ctx, j); err != nil {
		// The work is done and the acknowledgement failed, so the job runs
		// again when the lease expires. That is at-least-once behaving as
		// documented, and why a handler has to tolerate running twice.
		log.Error("acknowledging the job failed; it will run again", "error", err)
		return
	}

	log.Info("job done",
		"duration_ms", duration.Milliseconds(),
		"queries", len(col.Queries),
		"sql_ms", col.QueryTime().Milliseconds())
}
