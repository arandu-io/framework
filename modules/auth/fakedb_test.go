package auth_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
)

// A minimal database/sql driver, so the service can be exercised without a
// server. It is written here rather than pulled from a mock library because the
// core has one dependency and a test dependency is still a dependency -- the
// same reason data/fakedb_test.go exists.
//
// It is deliberately not a database: it records the statements and the
// arguments that reached it, which is what a test about authorization and
// hashing needs to assert on.

func init() { sql.Register("arandu-fake-auth", fakeDriver{}) }

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) { return &fakeConn{db: lookupFake(dsn)}, nil }

var (
	fakeMu  sync.Mutex
	fakeDBs = map[string]*fakeDB{}
	fakeSeq int
)

// fakeDB is the state a test inspects: every statement that arrived, with its
// arguments.
type fakeDB struct {
	mu    sync.Mutex
	stmts []string
	args  [][]driver.NamedValue
}

func newFakeDB() (*sql.DB, *fakeDB) {
	fakeMu.Lock()
	fakeSeq++
	dsn := fmt.Sprintf("fake-auth-%d", fakeSeq)
	state := &fakeDB{}
	fakeDBs[dsn] = state
	fakeMu.Unlock()

	db, err := sql.Open("arandu-fake-auth", dsn)
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

func (f *fakeDB) record(query string, args []driver.NamedValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stmts = append(f.stmts, strings.Join(strings.Fields(query), " "))
	f.args = append(f.args, args)
}

// statements returns what the driver was asked to run, whitespace collapsed so
// a test can match on shape rather than on indentation.
func (f *fakeDB) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stmts...)
}

// argsOf returns the arguments of the first statement containing substring, and
// reports whether there was one.
func (f *fakeDB) argsOf(substring string) ([]driver.NamedValue, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.stmts {
		if strings.Contains(s, substring) {
			return f.args[i], true
		}
	}
	return nil, false
}

type fakeConn struct{ db *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return fakeTx{}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.db.record(query, args)
	return fakeResult{}, nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.db.record(query, args)
	return emptyRows{}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

// emptyRows answers every read with "no rows", which is what a lookup before an
// insert should find.
type emptyRows struct{}

func (emptyRows) Columns() []string         { return []string{"id"} }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }
