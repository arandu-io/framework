package events_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/events"
)

// A minimal database/sql driver holding an outbox in memory, so the relay can be
// run without a server. It is written here rather than pulled from a mock
// library because the core has one dependency and a test dependency is still a
// dependency -- the same reason data/fakedb_test.go exists.
//
// It answers the four statements the outbox issues and nothing else. It is not
// a database and does not try to be one: what it gives a test is the ability to
// say "there is an event, five minutes old" and watch what the relay does.

func init() { sql.Register("arandu-fake-outbox", fakeDriver{}) }

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	return &fakeConn{table: lookupTable(dsn)}, nil
}

var (
	tablesMu sync.Mutex
	tables   = map[string]*fakeOutbox{}
	tableSeq int
)

// fakeOutbox is the state a test sets up and asserts on.
type fakeOutbox struct {
	mu        sync.Mutex
	pending   []events.Stored
	parked    []events.Stored
	published []string

	// failLag makes the lag query fail, which is how a health check reports a
	// database that is not answering rather than an outbox that is behind.
	failLag bool
}

func newFakeOutbox() (*sql.DB, *fakeOutbox) {
	tablesMu.Lock()
	tableSeq++
	dsn := fmt.Sprintf("fake-outbox-%d", tableSeq)
	table := &fakeOutbox{}
	tables[dsn] = table
	tablesMu.Unlock()

	db, err := sql.Open("arandu-fake-outbox", dsn)
	if err != nil {
		panic(err)
	}
	return db, table
}

func lookupTable(dsn string) *fakeOutbox {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	return tables[dsn]
}

// add appends an unpublished event that occurred age ago.
func (f *fakeOutbox) add(name string, age time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, events.Stored{
		ID:           fmt.Sprintf("evt-%d", len(f.pending)+len(f.parked)+1),
		TenantID:     tenant,
		Name:         name,
		Aggregate:    "invoice",
		AggregateID:  "inv-1",
		Payload:      `{}`,
		AuthorizedBy: "system",
		Action:       "outbox.store",
		OccurredAt:   time.Now().UTC().Add(-age),
	})
}

// park appends an event that already gave up.
func (f *fakeOutbox) park(name, lastError string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parked = append(f.parked, events.Stored{
		ID:           fmt.Sprintf("evt-%d", len(f.pending)+len(f.parked)+1),
		TenantID:     tenant,
		Name:         name,
		Aggregate:    "invoice",
		AggregateID:  "inv-1",
		Payload:      `{}`,
		AuthorizedBy: "system",
		Action:       "outbox.store",
		OccurredAt:   time.Now().UTC().Add(-time.Hour),
		Attempts:     10,
		LastError:    lastError,
	})
}

func (f *fakeOutbox) delivered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.published...)
}

func (f *fakeOutbox) breakTheLagQuery() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failLag = true
}

// errUnreachable is what a database that is not answering looks like from here.
var errUnreachable = errors.New("connection refused")

type fakeConn struct{ table *fakeOutbox }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return fakeTx{}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	f := c.table
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.Contains(query, "SET published_at"):
		// MarkPublished(id): the last argument is the id.
		id, _ := args[len(args)-1].Value.(string)
		f.published = append(f.published, id)
		f.pending = withoutID(f.pending, id)

	case strings.Contains(query, "SET failed_at = ?"):
		// Park(id): the row leaves the pending scan and stays in the table.
		id, _ := args[len(args)-1].Value.(string)
		if e, found := findID(f.pending, id); found {
			e.Attempts++
			f.parked = append(f.parked, e)
			f.pending = withoutID(f.pending, id)
		}

	case strings.Contains(query, "SET attempts = attempts + 1"):
		// MarkFailed(id): it stays pending and is tried again next pass.
		id, _ := args[len(args)-1].Value.(string)
		for i := range f.pending {
			if f.pending[i].ID == id {
				f.pending[i].Attempts++
			}
		}
	}
	return fakeResult{}, nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	f := c.table
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.Contains(query, "SELECT occurred_at"):
		if f.failLag {
			return nil, errUnreachable
		}
		if len(f.pending) == 0 {
			return &lagRows{}, nil
		}
		oldest := f.pending[0].OccurredAt
		return &lagRows{at: &oldest}, nil

	case strings.Contains(query, "failed_at IS NOT NULL"):
		return &outboxRows{rows: append([]events.Stored(nil), f.parked...)}, nil

	default:
		return &outboxRows{rows: append([]events.Stored(nil), f.pending...)}, nil
	}
}

func findID(list []events.Stored, id string) (events.Stored, bool) {
	for _, e := range list {
		if e.ID == id {
			return e, true
		}
	}
	return events.Stored{}, false
}

func withoutID(list []events.Stored, id string) []events.Stored {
	out := list[:0:0]
	for _, e := range list {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return out
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

// lagRows answers the one-column query behind Outbox.Lag.
type lagRows struct {
	at   *time.Time
	done bool
}

func (*lagRows) Columns() []string { return []string{"occurred_at"} }
func (*lagRows) Close() error      { return nil }

func (r *lagRows) Next(dest []driver.Value) error {
	if r.at == nil || r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = *r.at
	return nil
}

// outboxRows answers the projection every outbox read shares, in its order.
type outboxRows struct {
	rows []events.Stored
	i    int
}

func (*outboxRows) Columns() []string {
	return []string{"id", "tenant_id", "event", "aggregate", "aggregate_id", "payload",
		"authorized_by", "action", "occurred_at", "attempts", "last_error"}
}

func (*outboxRows) Close() error { return nil }

func (r *outboxRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	e := r.rows[r.i]
	r.i++
	dest[0] = e.ID
	dest[1] = e.TenantID
	dest[2] = e.Name
	dest[3] = e.Aggregate
	dest[4] = e.AggregateID
	dest[5] = e.Payload
	dest[6] = e.AuthorizedBy
	dest[7] = e.Action
	dest[8] = e.OccurredAt
	dest[9] = int64(e.Attempts)
	if e.LastError == "" {
		dest[10] = nil
	} else {
		dest[10] = e.LastError
	}
	return nil
}
