package foundation

import (
	"context"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/bus"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/queue/jobs"
	qmiddleware "github.com/arandu-io/hesape/queue/middleware"
)

// settled is the store a job in this test came off: it accepts every
// settlement and records nothing, because what is asserted is the batch.
type settled struct{}

func (settled) ReleaseJob(context.Context, *jobs.Job, time.Duration) error { return nil }
func (settled) DeleteJob(context.Context, *jobs.Job) error                 { return nil }
func (settled) FailJob(context.Context, *jobs.Job, error) error            { return nil }

// TestTheWorkerReportsEveryJobToItsBatch: the registration queue:work runs
// puts the batch report outermost on the worker, so a batch whose handlers
// report nothing still finishes and fires its Then; with no handler there is
// no registration at all, which is what keeps queue:work refusing.
func TestTheWorkerReportsEveryJobToItsBatch(t *testing.T) {
	if reportingToBatches(nil, bus.NewMemory(), nil) != nil {
		t.Fatal("with no handler the registration must stay nil, so queue:work refuses an empty worker")
	}

	ctx, g := context.Background(), auth.SystemGrant("invoice.import", "acme")
	batches := bus.NewMemory()
	var done int
	var w *queue.Worker
	q := queue.NewSyncQueue(later{&w})
	w = queue.NewWorker(q, queue.WorkerOptions{})
	reportingToBatches(func(w *queue.Worker) {
		// The handler reports nothing: no bus.Handled anywhere.
		w.HandleFunc("invoice.import", func(context.Context, auth.Grant, *jobs.Job) error { return nil })
		w.HandleFunc("import.done", func(context.Context, auth.Grant, *jobs.Job) error { done++; return nil })
	}, batches, q)(w)

	if mw := w.Options().Middleware; len(mw) == 0 {
		t.Fatal("the worker runs no middleware")
	} else if _, ok := mw[0].(*qmiddleware.ReportToBatch); !ok {
		t.Fatalf("the outermost middleware is %T, want the batch report", mw[0])
	}

	pushed := &payloads{}
	b, err := bus.NewBatch("import").
		Add("invoice.import", nil).
		Add("invoice.import", nil).
		Then("import.done", nil).
		Dispatch(ctx, g, batches, pushed)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for _, payload := range pushed.all {
		j, err := jobs.New(g, "", "invoice.import", nil)
		if err != nil {
			t.Fatalf("jobs.New: %v", err)
		}
		j.Payload = payload
		if err := w.Process(ctx, jobs.Popped(settled{}, "test", j)); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}

	got, err := batches.Find(ctx, g, b.ID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.PendingJobs != 0 || done != 1 {
		t.Errorf("after both jobs succeeded the batch has %d pending and Then ran %d times, want 0 and 1", got.PendingJobs, done)
	}
}

// payloads is the queue the batch is dispatched on: it keeps what was pushed.
type payloads struct{ all [][]byte }

func (p *payloads) Push(_ context.Context, _ auth.Grant, _, _ string, payload []byte) error {
	p.all = append(p.all, payload)
	return nil
}

// later answers the handlers of a worker built after the queue it drains,
// which is the order a sync queue and its worker have to be built in.
type later struct{ w **queue.Worker }

func (l later) Handler(name string) (queue.Handler, bool) { return (*l.w).Handler(name) }
