package test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/hesape/database"
)

// A minimal database/sql driver, so the database assertions can be tested
// without a server. It is written here rather than pulled from a mock library
// because the core has two dependencies and a test dependency is still a
// dependency -- the same reason the database package carries its own.
//
// It answers two shapes and no others, which are the two these assertions
// send: a COUNT(*), answered with whatever the fixture set, and a SELECT *,
// answered with the fixture's rows. It parses nothing. What the statement says
// is tested against countQuery, where the statement is built; what is tested
// here is that the assertion asks the database and believes the answer.

func init() { sql.Register("arandutest-fake", fakeDriver{}) }

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) { return &fakeConn{db: lookupFake(dsn)}, nil }

var (
	fakeMu  sync.Mutex
	fakeDBs = map[string]*fakeDB{}
	fakeSeq int
)

// fakeDB is the state a fixture writes and a test inspects.
type fakeDB struct {
	mu sync.Mutex

	// count is what a COUNT(*) answers.
	count int

	// columns and rows are what a SELECT * answers.
	columns []string
	rows    [][]any

	// queries is every statement that arrived, after rebinding.
	queries []string
}

// openFake registers a fake and returns the pool over it plus its state.
func openFake(t *testing.T) (*sql.DB, *fakeDB) {
	t.Helper()

	fakeMu.Lock()
	fakeSeq++
	dsn := fmt.Sprintf("fake-%d", fakeSeq)
	state := &fakeDB{}
	fakeDBs[dsn] = state
	fakeMu.Unlock()

	inner, err := sql.Open("arandutest-fake", dsn)
	if err != nil {
		t.Fatalf("opening the fake: %v", err)
	}
	t.Cleanup(func() {
		_ = inner.Close()
		fakeMu.Lock()
		delete(fakeDBs, dsn)
		fakeMu.Unlock()
	})

	return inner, state
}

// newFakeDB returns the instrumented handle over a fake, plus its state.
func newFakeDB(t *testing.T) (*database.DB, *fakeDB) {
	t.Helper()

	inner, state := openFake(t)
	return database.Wrap(inner, database.DialectSQLite), state
}

// newFakeConnection returns a connection over a fake, plus its state. It is the
// handle that carries query listeners, which the instrumented one does not.
func newFakeConnection(t *testing.T) (*database.Connection, *fakeDB) {
	t.Helper()

	inner, state := openFake(t)
	return database.NewConnection(inner, "fake", "", nil), state
}

func lookupFake(dsn string) *fakeDB {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	return fakeDBs[dsn]
}

// lastQuery is the statement that arrived most recently, or the empty string
// when none did.
func (f *fakeDB) lastQuery() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return ""
	}
	return f.queries[len(f.queries)-1]
}

func (f *fakeDB) record(query string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
}

func (f *fakeDB) answer(query string) (*fakeRows, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.HasPrefix(query, "SELECT COUNT(*)") {
		return &fakeRows{columns: []string{"count"}, rows: [][]any{{int64(f.count)}}}, nil
	}
	return &fakeRows{columns: f.columns, rows: f.rows}, nil
}

type fakeConn struct{ db *fakeDB }

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	if c.db == nil {
		return nil, fmt.Errorf("no fake registered for this connection")
	}
	c.db.record(query)
	return &fakeStmt{db: c.db, query: query}, nil
}

func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("the fake has no transactions") }

type fakeStmt struct {
	db    *fakeDB
	query string
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, fmt.Errorf("the fake only answers queries")
}

func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) { return s.db.answer(s.query) }

type fakeRows struct {
	columns []string
	rows    [][]any
	at      int
}

func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.at >= len(r.rows) {
		return io.EOF
	}
	for i := range dest {
		if i < len(r.rows[r.at]) {
			dest[i] = r.rows[r.at][i]
		}
	}
	r.at++
	return nil
}
