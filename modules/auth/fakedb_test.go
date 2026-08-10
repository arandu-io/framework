package auth_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/modules/auth"
)

// A minimal database/sql driver, so the service can be exercised without a
// server. It is written here rather than pulled from a mock library because the
// core has one dependency and a test dependency is still a dependency -- the
// same reason data/fakedb_test.go exists.
//
// It is deliberately not a database: it records the statements and the
// arguments that reached it, which is what a test about authorization and
// hashing needs to assert on.
//
// It answers two families of read on top of that -- the users projection and the
// outbox projection -- because the events this module publishes are only proved
// by a subscriber receiving them, and a subscriber reads the outbox through the
// relay. Anything it is not asked about still answers "no rows", which is what
// every test written before this one expects.

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

	// users are the rows a SELECT on the users table can find. Empty means every
	// lookup answers "no rows", which is what a test about an insert wants.
	users []auth.User
	// outbox is what the module stored, and what the relay reads back.
	outbox    []events.Stored
	published map[string]bool

	// breakOutbox makes the outbox insert fail, which is how a test asks what
	// happens to the write when its event cannot be recorded.
	breakOutbox bool

	// breakUsers makes every read of the users table fail, which is how a test
	// asks what a sign-in does when the database is down rather than when the
	// password is wrong. The two must not be confused: one is a failed attempt
	// and the other is an outage.
	breakUsers bool

	// confirmOnRead stamps the seeded row as verified while a read of the users
	// table is being answered, and hands back the row as it was. It is the one
	// interleaving a real database produces and a sequential test cannot: the
	// person's click and their mail scanner's prefetch, both reading an
	// unverified row before either of them writes.
	confirmOnRead bool
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

// seedUser makes a row that the lookups by id and by address will find.
func (f *fakeDB) seedUser(u auth.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users = append(f.users, u)
}

// breakTheOutbox makes every outbox insert fail from now on.
func (f *fakeDB) breakTheOutbox() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.breakOutbox = true
}

// confirmBehindOurBack makes the next read of the users table hand back an
// unverified row and leave a verified one behind it, which is what the service
// sees when another click lands between its read and its write.
func (f *fakeDB) confirmBehindOurBack() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmOnRead = true
}

// usersTableFails switches every read of the users table between answering and
// refusing, so one test can watch a sign-in through an outage and out the other
// side of it.
func (f *fakeDB) usersTableFails(broken bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.breakUsers = broken
}

// errUnreachable is what a table that is not there looks like from here.
var errUnreachable = errors.New("no such table: outbox")

// errUsersUnreachable is the same thing for the table the sign-in reads.
var errUsersUnreachable = errors.New("no such table: users")

// findUser answers a lookup whose first argument is an id or an address.
//
// The tenant is the second argument of both lookups, and it is matched here
// rather than ignored: two customers may hold the same address, and a fake that
// answered with whichever row it saw first would let a test about tenant
// isolation pass while the code under it resolved by address alone.
func (f *fakeDB) findUser(args []driver.NamedValue) (auth.User, bool) {
	if len(args) == 0 {
		return auth.User{}, false
	}
	key, _ := args[0].Value.(string)
	var tenant string
	if len(args) > 1 {
		tenant, _ = args[1].Value.(string)
	}
	for _, u := range f.users {
		if u.ID != key && u.Email != key {
			continue
		}
		if tenant != "" && u.TenantID != tenant {
			continue
		}
		return u, true
	}
	return auth.User{}, false
}

// changeAddress moves a seeded account to another address, which is what a link
// already in an inbox has to survive being told about.
func (f *fakeDB) changeAddress(id, email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.users {
		if f.users[i].ID == id {
			f.users[i].Email = auth.NormalizeEmail(email)
		}
	}
}

// confirmStored is the other click's write, applied to the stored row. The
// caller already holds the lock.
func (f *fakeDB) confirmStored(id string) {
	for i := range f.users {
		if f.users[i].ID == id {
			f.users[i].VerifiedAt = time.Now().UTC()
		}
	}
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

	f := c.db
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.Contains(query, "INSERT INTO outbox"):
		if f.breakOutbox {
			return nil, errUnreachable
		}
		f.outbox = append(f.outbox, storedFrom(args))

	case strings.Contains(query, "UPDATE outbox SET published_at"):
		id, _ := args[len(args)-1].Value.(string)
		if f.published == nil {
			f.published = map[string]bool{}
		}
		f.published[id] = true

	case strings.Contains(query, "UPDATE users SET password"):
		// Applied to the stored row rather than merely counted, because the
		// reset link's single-use property IS the stored hash: a fake that
		// accepted the write and kept the old password would report a link as
		// spent-proof while it still worked.
		hash, _ := args[0].Value.(string)
		id, _ := args[1].Value.(string)
		tenant, _ := args[2].Value.(string)
		for i := range f.users {
			if f.users[i].ID == id && f.users[i].TenantID == tenant {
				f.users[i].Password = hash
				return affected, nil
			}
		}
		return fakeResult{}, nil

	case strings.Contains(query, "UPDATE users SET verified_at"):
		// The conditional flip, counted the way a database counts it: one row
		// while the column is still null, none once somebody else stamped it.
		// Answering "one row" unconditionally is what would let the duplicate
		// event this statement exists to prevent pass the tests below.
		at, _ := args[0].Value.(time.Time)
		id, _ := args[1].Value.(string)
		for i := range f.users {
			if f.users[i].ID == id && f.users[i].VerifiedAt.IsZero() {
				f.users[i].VerifiedAt = at
				return affected, nil
			}
		}
		return fakeResult{}, nil
	}
	return affected, nil
}

// storedFrom reads the outbox insert back into the row it wrote. The order is
// the one in events.Outbox.Store, and a change there breaks this loudly.
func storedFrom(args []driver.NamedValue) events.Stored {
	text := func(i int) string {
		if i >= len(args) {
			return ""
		}
		s, _ := args[i].Value.(string)
		return s
	}
	e := events.Stored{
		ID: text(0), TenantID: text(1), Name: text(2), Aggregate: text(3),
		AggregateID: text(4), Payload: text(5), AuthorizedBy: text(6), Action: text(7),
	}
	if len(args) > 8 {
		e.OccurredAt, _ = args[8].Value.(time.Time)
	}
	return e
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.db.record(query, args)

	f := c.db
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.Contains(query, "FROM outbox"):
		var pending []events.Stored
		for _, e := range f.outbox {
			if !f.published[e.ID] {
				pending = append(pending, e)
			}
		}
		return &outboxRows{rows: pending}, nil

	case strings.Contains(query, "FROM users"):
		if f.breakUsers {
			return nil, errUsersUnreachable
		}
		// Only when there is a match: a miss answers with the shape every test
		// written before the users table was seedable expects.
		if u, found := f.findUser(args); found {
			if f.confirmOnRead {
				f.confirmOnRead = false
				f.confirmStored(u.ID)
			}
			return &userRows{rows: []auth.User{u}}, nil
		}
	}
	return emptyRows{}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

// fakeResult carries the row count, because two callers decide on it: Update
// reads zero as "no such user", and Confirm reads zero as "somebody else got
// here first".
type fakeResult struct{ rows int64 }

// affected is the answer every statement gets unless it says otherwise.
var affected = fakeResult{rows: 1}

func (fakeResult) LastInsertId() (int64, error)   { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, nil }

// emptyRows answers every read with "no rows", which is what a lookup before an
// insert should find.
type emptyRows struct{}

func (emptyRows) Columns() []string         { return []string{"id"} }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }

// userRows answers the projection every read of the users table shares, in the
// order userColumns declares it.
type userRows struct {
	rows []auth.User
	i    int
}

func (*userRows) Columns() []string {
	return []string{"id", "tenant_id", "name", "email", "password", "roles",
		"verified_at", "created_at"}
}

func (*userRows) Close() error { return nil }

func (r *userRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	u := r.rows[r.i]
	r.i++
	dest[0] = u.ID
	dest[1] = u.TenantID
	if u.Name == "" {
		dest[2] = nil
	} else {
		dest[2] = u.Name
	}
	dest[3] = u.Email
	dest[4] = u.Password
	// The roles the row was seeded with, as the JSON the column holds. It used to
	// be a literal `[]` whatever was seeded, so every read handed back an account
	// with no roles -- and a test asking whether a write preserved them was
	// answered by the fake rather than by the code.
	dest[5] = rolesJSON(u.Roles)
	if u.VerifiedAt.IsZero() {
		dest[6] = nil
	} else {
		dest[6] = u.VerifiedAt
	}
	dest[7] = u.CreatedAt
	return nil
}

// rolesJSON is the roles column: JSON text, never null, because that is what
// UserRepo writes and what scanUser unmarshals.
func rolesJSON(roles []string) []byte {
	if len(roles) == 0 {
		return []byte(`[]`)
	}
	out, err := json.Marshal(roles)
	if err != nil {
		return []byte(`[]`)
	}
	return out
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
	dest[10] = nil
	return nil
}
