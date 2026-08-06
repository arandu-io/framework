package jobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
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
	// Recorder receives each finished job, so it shows on /_arandu/debug with
	// its queries and its timeline -- exactly like a request.
	//
	// Nil means no instrumentation, and that is what production looks like: no
	// Collector is built, FromContext returns nil, and every Record method is a
	// no-op on a nil receiver. Zero cost, not low cost.
	//
	// It used to build a Collector on every job unconditionally and then throw
	// it away -- so production paid for recording every query with its bound
	// arguments and its caller frames, and nobody could read any of it. Found by
	// audit. Pass kernel.Recorder() to turn it on.
	Recorder *observability.Recorder
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

		// The batch runs in parallel, because the lease was taken for all of it
		// at once.
		//
		// It used to run serially, which made Concurrency a lie in the one way
		// that costs data: Reserve hid all n jobs for the same Lease, so with
		// Concurrency 4, Lease 5m and a two-minute handler, the fourth job
		// started at minute six -- past its own lease, already visible to
		// another worker, and running in both. At-least-once turned into
		// exactly-twice for the tail of every batch. Found by audit.
		//
		// The batch is sized by Concurrency, so running all of it at once is
		// exactly Concurrency jobs in flight, not Concurrency squared.
		var wg sync.WaitGroup
		for _, j := range reserved {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.runOne(ctx, j)
			}()
		}
		wg.Wait()
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

// runOne executes a job, instrumented when there is somewhere to send it.
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

	// Only when a recorder is wired. See WorkerOptions.Recorder.
	var col *observability.Collector
	if w.opts.Recorder != nil {
		col = observability.NewCollector(j.ID)
		jobCtx = observability.WithCollector(jobCtx, col)
	}
	log := observability.Log(jobCtx).With("job", j.Name, "job_id", j.ID, "tenant", j.TenantID)
	jobCtx = observability.WithLogger(jobCtx, log)

	start := time.Now()
	err := handler.Handle(jobCtx, GrantFor(j), j)
	duration := time.Since(start)

	if col != nil {
		// Method and Path name the job rather than a route, so the console list
		// reads "job invoice.send" next to "GET /invoices".
		w.opts.Recorder.Record(observability.Recorded{
			RequestID: j.ID,
			Method:    "job",
			Path:      j.Name,
			Duration:  duration,
			At:        start,
			Collector: col,
		})
	}

	if err != nil {
		// Attempts already counts this delivery -- Reserve incremented it. Adding
		// one here counted it twice, so MaxAttempts of N delivered N-1 times and
		// MaxAttempts of 2 parked on the first failure with no retry at all.
		// Found by audit; the in-memory queue used by the worker tests did not
		// increment, which is why it never showed up here.
		attempts := j.Attempts
		if attempts < 1 {
			attempts = 1
		}
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
		"queries", col.QueryCount(),
		"sql_ms", col.QueryTime().Milliseconds())
}
