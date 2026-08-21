// Tests of the module, which is an envelope rather than a bridge over a name.
//
// What Health decides and what Diagnose says is tested in hesape, against the
// code that runs; what is left to prove here is that the envelope reaches it --
// that Routes takes this framework's router and registers nothing on it, and
// that the three delegated answers are the inner module's rather than a second
// copy of them.
//
// Migrations gets the most attention because it is the one that can be wrong in
// silence. The schema is discovered by asking the driver, so anything standing
// between the module and the driver hides a Migratable driver behind itself and
// answers nil -- a jobs table that is never created, on a queue that otherwise
// looks wired.

package feature

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/jobs"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/migrations"
	hjobs "github.com/arandu-io/hesape/queue/jobs"
)

// hesapeQueue is a driver of the shape hesape/queue.Queue describes, which is
// what the module takes. Only the three questions the module asks are answered
// with anything; the rest are here because the interface has them.
type hesapeQueue struct {
	oldest time.Time
	// pending is what PendingSize reports, and failed is the dead letter list.
	pending int
	failed  []hjobs.Job
	// err, when set, is what the three questions answer with instead.
	err error
}

func (q *hesapeQueue) Push(context.Context, security.Grant, hjobs.Job) error { return nil }
func (q *hesapeQueue) PushOn(context.Context, security.Grant, string, hjobs.Job) error {
	return nil
}

func (q *hesapeQueue) Later(context.Context, security.Grant, time.Duration, hjobs.Job) error {
	return nil
}

func (q *hesapeQueue) Bulk(context.Context, security.Grant, []hjobs.Job) error { return nil }

func (q *hesapeQueue) Pop(context.Context, string, int, time.Duration) ([]*hjobs.Job, error) {
	return nil, nil
}

func (q *hesapeQueue) Size(context.Context, string) (int, error) { return 0, nil }

func (q *hesapeQueue) PendingSize(context.Context, string) (int, error) {
	return q.pending, q.err
}

func (q *hesapeQueue) CreationTimeOfOldestPendingJob(context.Context, string) (time.Time, error) {
	return q.oldest, q.err
}

func (q *hesapeQueue) Clear(context.Context, string) (int, error) { return 0, nil }

func (q *hesapeQueue) Failed(context.Context, int) ([]hjobs.Job, error) {
	return q.failed, q.err
}

func (q *hesapeQueue) Retry(context.Context, string) error { return nil }

// migratingQueue is a driver that keeps jobs in the application's database, and
// therefore carries the table it reads.
type migratingQueue struct{ hesapeQueue }

func (q *migratingQueue) Migrations() []kernel.Migration {
	return []kernel.Migration{createTestJobsTable{}}
}

// createTestJobsTable stands in for the schema a database-backed driver
// declares. What it creates does not matter here; that it arrives does.
type createTestJobsTable struct{ migrations.BaseMigration }

func (createTestJobsTable) GetName() string { return "2026_08_20_000001_create_jobs_table" }

func (createTestJobsTable) Up(ctx context.Context, conn migrations.Connection) error {
	_, err := conn.Statement(ctx, `CREATE TABLE jobs (id VARCHAR(255) PRIMARY KEY)`, nil)
	return err
}

// TestTheModuleAnswersTheFrameworkContract is the reason this type exists.
//
// The inner module names a router this framework does not use, so it cannot be
// registered through the contract the kernel collects. This asserts the
// signature that replaced it, and that it stays a module with no HTTP surface:
// a route registered here would be one nobody asked this package for.
func TestTheModuleAnswersTheFrameworkContract(t *testing.T) {
	var m kernel.Module = jobs.NewModule(&hesapeQueue{})

	r := http.NewRouter()
	before := len(r.Routes())
	m.Routes(r)
	if got := len(r.Routes()) - before; got != 0 {
		t.Errorf("the module registered %d route(s); it has no HTTP surface", got)
	}

	if m.Name() != "queue" {
		t.Errorf("the module names itself %q", m.Name())
	}
}

// TestMigrationsReachTheDriver is the answer that can be wrong in silence.
//
// A driver holding the jobs table has to have it collected, and a driver that
// keeps jobs elsewhere has to contribute nothing rather than an empty table it
// will never read. Both halves are asserted, because a delegation that always
// returned nil would pass with only the second.
func TestMigrationsReachTheDriver(t *testing.T) {
	declared := jobs.NewModule(&migratingQueue{}).Migrations()
	if len(declared) != 1 {
		t.Fatalf("the driver's schema did not reach the module: %d migration(s)", len(declared))
	}
	if got := declared[0].GetName(); got != "2026_08_20_000001_create_jobs_table" {
		t.Errorf("the migration came back as %q", got)
	}

	if declared := jobs.NewModule(&hesapeQueue{}).Migrations(); declared != nil {
		t.Errorf("a driver that stores jobs elsewhere contributed %d migration(s)", len(declared))
	}
}

// TestHealthFailsWhenNothingIsDraining: a stopped worker looks exactly like an
// idle one, and the age of the oldest waiting job is what tells them apart.
//
// The threshold is the inner module's, so what is asserted is the two ends of
// it: a queue nothing is waiting on is healthy, and one waiting an hour is not.
func TestHealthFailsWhenNothingIsDraining(t *testing.T) {
	idle := jobs.NewModule(&hesapeQueue{})
	if err := idle.Health(context.Background()); err != nil {
		t.Errorf("a queue with nothing waiting was reported unhealthy: %v", err)
	}

	stalled := jobs.NewModule(&hesapeQueue{oldest: time.Now().Add(-time.Hour), pending: 3})
	err := stalled.Health(context.Background())
	if err == nil {
		t.Fatal("a queue whose oldest job has waited an hour was reported healthy")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Errorf("the failure does not say what to look at: %q", err)
	}

	// A driver that cannot answer is a failure and not a healthy queue: the
	// alternative reads an unreachable store as an empty one.
	broken := jobs.NewModule(&hesapeQueue{err: errors.New("the connection is closed")})
	if err := broken.Health(context.Background()); err == nil {
		t.Error("a driver that could not be asked was reported healthy")
	}
}

// TestDiagnoseReachesTheInnerModule covers the sentences the error page prints,
// next to the failure somebody is already looking at.
//
// Nothing wrong has to produce nothing: a diagnosis that always says something
// is a diagnosis nobody reads.
func TestDiagnoseReachesTheInnerModule(t *testing.T) {
	if hints := jobs.NewModule(&hesapeQueue{}).Diagnose(context.Background()); len(hints) != 0 {
		t.Errorf("a queue with nothing wrong produced %v", hints)
	}

	m := jobs.NewModule(&hesapeQueue{
		oldest:  time.Now().Add(-time.Hour),
		pending: 3,
		failed:  []hjobs.Job{{Name: "invoice.send", LastError: "the invoice has no address"}},
	})

	all := strings.Join(m.Diagnose(context.Background()), "\n")
	for _, want := range []string{"3 job(s) waiting", "invoice.send", "the invoice has no address"} {
		if !strings.Contains(all, want) {
			t.Errorf("the diagnosis does not mention %q:\n%s", want, all)
		}
	}
}

// TestTheWatchedQueuesAreTheOnesNamed: a queue nobody watches can stop draining
// without anyone noticing, and naming none has to keep watching the default one
// rather than nothing at all.
func TestTheWatchedQueuesAreTheOnesNamed(t *testing.T) {
	stalled := &hesapeQueue{oldest: time.Now().Add(-time.Hour), pending: 1}

	if err := jobs.NewModule(stalled).Health(context.Background()); err == nil {
		t.Error("naming no queue watched nothing: the default queue went unwatched")
	} else if !strings.Contains(err.Error(), jobs.DefaultQueue) {
		t.Errorf("the failure names %q rather than the default queue", err)
	}

	err := jobs.NewModule(stalled, "invoices", "reports").Health(context.Background())
	if err == nil {
		t.Fatal("a named queue that stopped draining was reported healthy")
	}
	if !strings.Contains(err.Error(), "invoices") {
		t.Errorf("the failure does not name the queue it is about: %q", err)
	}
}
