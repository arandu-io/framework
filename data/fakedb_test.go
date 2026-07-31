package data_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// A minimal database/sql driver, so the data package can be tested without a
// server. It is here rather than pulled from a mock library because the core has
// two dependencies and a test dependency is still a dependency.

func init() { sql.Register("arandu-fake", fakeDriver{}) }

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	return &fakeConn{db: lookupFake(dsn)}, nil
}

var (
	fakeMu   sync.Mutex
	fakeDBs  = map[string]*fakeDB{}
	fakeSeq  int
	fakeName = "fake-%d"
)

// fakeDB is the state a fixture inspects: every statement that arrived, and the
// rows to answer with.
type fakeDB struct {
	mu      sync.Mutex
	stmts   []string
	args    [][]driver.NamedValue
	rows    map[string][]string // sql substring -> single column rows
	failOn  string
	failErr error
}

// newFakeDB registers a fake and returns the handle plus its state.
func newFakeDB() (*sql.DB, *fakeDB) {
	fakeMu.Lock()
	fakeSeq++
	dsn := fmt.Sprintf(fakeName, fakeSeq)
	state := &fakeDB{rows: map[string][]string{}}
	fakeDBs[dsn] = state
	fakeMu.Unlock()

	db, err := sql.Open("arandu-fake", dsn)
	if err != nil {
		panic(err)
	}
	return db, state
}

func lookupFake(dsn string) *fakeDB {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	return fakeDBs[dsn]
}

func (f *fakeDB) record(query string, args []driver.NamedValue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stmts = append(f.stmts, query)
	f.args = append(f.args, args)
	if f.failOn != "" && strings.Contains(query, f.failOn) {
		return f.failErr
	}
	return nil
}

// statements returns the statements seen so far, whitespace collapsed so a test
// can match on shape without matching on indentation.
func (f *fakeDB) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.stmts))
	for i, s := range f.stmts {
		out[i] = strings.Join(strings.Fields(s), " ")
	}
	return out
}

func (f *fakeDB) sawStatement(substring string) bool {
	for _, s := range f.statements() {
		if strings.Contains(s, substring) {
			return true
		}
	}
	return false
}

func (f *fakeDB) rowsFor(query string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for pattern, rows := range f.rows {
		if strings.Contains(query, pattern) {
			return rows
		}
	}
	return nil
}

type fakeConn struct{ db *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return &fakeTx{db: c.db}, nil }

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &fakeTx{db: c.db}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.db.record(query, args); err != nil {
		return nil, err
	}
	return fakeResult{}, nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.db.record(query, args); err != nil {
		return nil, err
	}
	return &fakeRows{values: c.db.rowsFor(query)}, nil
}

type fakeTx struct{ db *fakeDB }

func (t *fakeTx) Commit() error   { return t.db.record("COMMIT", nil) }
func (t *fakeTx) Rollback() error { return t.db.record("ROLLBACK", nil) }

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

// fakeRows answers the shape of the migrations table: id, batch, applied_at.
// One shape is enough because it is the only query the data package issues on
// its own -- repository queries belong to the modules that own them.
type fakeRows struct {
	values []string
	i      int
}

func (r *fakeRows) Columns() []string { return []string{"id", "batch", "applied_at"} }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.values) {
		return io.EOF
	}
	dest[0] = r.values[r.i]
	if len(dest) > 1 {
		dest[1] = int64(1)
		dest[2] = time.Unix(0, 0).UTC()
	}
	r.i++
	return nil
}
