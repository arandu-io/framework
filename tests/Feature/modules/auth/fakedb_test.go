package feature

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

	// rowsAffectedErr makes successful statements unable to report their row
	// count. A statement may have reached the database and still leave its
	// caller unable to decide whether it changed the intended row.
	rowsAffectedErr error
	// rowsAffected overrides the count for tests about a statement that matched
	// no row. Nil keeps each statement's natural count.
	rowsAffected *int64

	// users are the rows a SELECT on the users table can find. Empty means every
	// lookup answers "no rows", which is what a test about an insert wants.
	users []auth.User

	// twoFactor and recoveryCodes are the second factor's two tables, and they
	// are stored rather than merely counted for the reason the password column
	// is: the properties under test ARE what is left behind. A fake that
	// accepted the writes and remembered nothing would report an enrolment as
	// confirmed, a step as burned and a recovery code as spent, all without any
	// of it being true, and every test below would pass against code that does
	// none of it.
	//
	// The two conditions in the statements are honoured here, and they are the
	// whole point: `confirmed_at IS NULL` is what refuses a second enrolment
	// over a live one, `last_used_step < ?` is what refuses a replayed code and
	// `used_at IS NULL` is what spends a recovery code exactly once. Answering
	// "one row" unconditionally would let every one of those through.
	twoFactor     []twoFactorRow
	recoveryCodes []recoveryCodeRow
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

// rowsAffectedFails makes every result refuse to report its row count.
func (f *fakeDB) rowsAffectedFails(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsAffectedErr = err
}

// reportsRowsAffected overrides the count returned by every statement.
func (f *fakeDB) reportsRowsAffected(rows int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsAffected = &rows
}

// result builds a statement result while the caller holds f.mu.
func (f *fakeDB) result(rows int64) fakeResult {
	if f.rowsAffected != nil {
		rows = *f.rowsAffected
	}
	return fakeResult{rows: rows, err: f.rowsAffectedErr}
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

// twoFactorRow is one account's enrolment as the fake stores it.
type twoFactorRow struct {
	userID       string
	tenantID     string
	secret       string
	confirmedAt  time.Time
	lastUsedStep int64
	createdAt    time.Time
}

// recoveryCodeRow is one recovery code as the fake stores it: the hash, and
// when it was spent.
type recoveryCodeRow struct {
	id       string
	tenantID string
	userID   string
	hash     string
	usedAt   time.Time
}

// errDuplicateTwoFactor is what inserting a second enrolment for one account
// looks like from here. The wording is SQLite's, because that is one of the
// three spellings the repository recognises a duplicate key by.
var errDuplicateTwoFactor = errors.New("UNIQUE constraint failed: user_two_factor.user_id")

// storedEvents returns the outbox as the relay would find it.
//
// Read straight out of the table rather than through a subscriber, because what
// the tests using it ask is what was WRITTEN: an event that carries key
// material carries it in the row, whether or not anything ever delivers it.
func (f *fakeDB) storedEvents() []events.Stored {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]events.Stored(nil), f.outbox...)
}

// enrolmentFor returns the stored enrolment of one account. The caller holds no
// lock; this takes it.
func (f *fakeDB) enrolmentFor(userID, tenant string) (twoFactorRow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.twoFactor {
		if row.userID == userID && row.tenantID == tenant {
			return row, true
		}
	}
	return twoFactorRow{}, false
}

// unusedRecoveryCodes counts the codes of one account that have not been spent.
func (f *fakeDB) unusedRecoveryCodes(userID, tenant string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, row := range f.recoveryCodes {
		if row.userID == userID && row.tenantID == tenant && row.usedAt.IsZero() {
			n++
		}
	}
	return n
}

// text reads one driver argument as a string, and answers "" for an argument
// that is not there. The statements below name their arguments by position, and
// a test that mis-counted should fail on the assertion rather than on an index.
func text(args []driver.NamedValue, i int) string {
	if i >= len(args) {
		return ""
	}
	s, _ := args[i].Value.(string)
	return s
}

// number reads one driver argument as an integer, which is how the spent time
// step travels.
func number(args []driver.NamedValue, i int) int64 {
	if i >= len(args) {
		return 0
	}
	n, _ := args[i].Value.(int64)
	return n
}

// execTwoFactor answers the statements of the second factor's two tables, and
// reports whether the statement was one of them. The caller holds f.mu.
func (f *fakeDB) execTwoFactor(query string, args []driver.NamedValue) (driver.Result, error, bool) {
	switch {
	case strings.Contains(query, "DELETE FROM user_two_factor"):
		// The conditional form removes an enrolment that was begun and never
		// confirmed; the plain form is the disable, and removes either.
		onlyUnconfirmed := strings.Contains(query, "confirmed_at IS NULL")
		userID, tenant := text(args, 0), text(args, 1)
		kept := f.twoFactor[:0]
		removed := int64(0)
		for _, row := range f.twoFactor {
			if row.userID == userID && row.tenantID == tenant &&
				(!onlyUnconfirmed || row.confirmedAt.IsZero()) {
				removed++
				continue
			}
			kept = append(kept, row)
		}
		f.twoFactor = kept
		return f.result(removed), nil, true

	case strings.Contains(query, "INSERT INTO user_two_factor"):
		row := twoFactorRow{
			userID: text(args, 0), tenantID: text(args, 1), secret: text(args, 2),
			lastUsedStep: number(args, 4),
		}
		if at, ok := args[3].Value.(time.Time); ok {
			row.confirmedAt = at
		}
		if at, ok := args[5].Value.(time.Time); ok {
			row.createdAt = at
		}
		for _, existing := range f.twoFactor {
			if existing.userID == row.userID {
				// The primary key, enforced. It is what turns "the delete above
				// spared a confirmed enrolment" into a refusal, and without it
				// the fake would let a live second factor be replaced by an
				// unconfirmed secret.
				return nil, errDuplicateTwoFactor, true
			}
		}
		f.twoFactor = append(f.twoFactor, row)
		return f.result(1), nil, true

	case strings.Contains(query, "UPDATE user_two_factor SET confirmed_at"):
		// The condition is read off the statement rather than assumed, here and
		// in the three below. A fake that enforces a WHERE clause the query no
		// longer contains is a fake that keeps the property alive after the code
		// has dropped it -- which makes every test of that property pass against
		// the change that breaks it.
		onlyUnconfirmed := strings.Contains(query, "confirmed_at IS NULL")
		at, _ := args[0].Value.(time.Time)
		userID, tenant := text(args, 1), text(args, 2)
		for i := range f.twoFactor {
			row := &f.twoFactor[i]
			if row.userID == userID && row.tenantID == tenant &&
				(!onlyUnconfirmed || row.confirmedAt.IsZero()) {
				row.confirmedAt = at
				return f.result(1), nil, true
			}
		}
		return f.result(0), nil, true

	case strings.Contains(query, "UPDATE user_two_factor SET last_used_step"):
		onlyHigher := strings.Contains(query, "last_used_step < ?")
		step := number(args, 0)
		userID, tenant := text(args, 1), text(args, 2)
		for i := range f.twoFactor {
			row := &f.twoFactor[i]
			if row.userID == userID && row.tenantID == tenant &&
				(!onlyHigher || row.lastUsedStep < step) {
				row.lastUsedStep = step
				return f.result(1), nil, true
			}
		}
		return f.result(0), nil, true

	case strings.Contains(query, "DELETE FROM user_recovery_codes"):
		userID, tenant := text(args, 0), text(args, 1)
		kept := f.recoveryCodes[:0]
		removed := int64(0)
		for _, row := range f.recoveryCodes {
			if row.userID == userID && row.tenantID == tenant {
				removed++
				continue
			}
			kept = append(kept, row)
		}
		f.recoveryCodes = kept
		return f.result(removed), nil, true

	case strings.Contains(query, "INSERT INTO user_recovery_codes"):
		f.recoveryCodes = append(f.recoveryCodes, recoveryCodeRow{
			id: text(args, 0), tenantID: text(args, 1), userID: text(args, 2),
			hash: text(args, 3),
		})
		return f.result(1), nil, true

	case strings.Contains(query, "UPDATE user_recovery_codes SET used_at"):
		onlyUnspent := strings.Contains(query, "used_at IS NULL")
		at, _ := args[0].Value.(time.Time)
		id, tenant := text(args, 1), text(args, 2)
		for i := range f.recoveryCodes {
			row := &f.recoveryCodes[i]
			if row.id == id && row.tenantID == tenant && (!onlyUnspent || row.usedAt.IsZero()) {
				row.usedAt = at
				return f.result(1), nil, true
			}
		}
		return f.result(0), nil, true
	}
	return nil, nil, false
}

// twoFactorRows answers the projection every read of the enrolment shares, in
// the order the repository declares it.
type twoFactorRows struct {
	rows []twoFactorRow
	i    int
}

func (*twoFactorRows) Columns() []string {
	return []string{"user_id", "tenant_id", "secret", "confirmed_at", "last_used_step", "created_at"}
}

func (*twoFactorRows) Close() error { return nil }

func (r *twoFactorRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.i]
	r.i++
	dest[0] = row.userID
	dest[1] = row.tenantID
	dest[2] = row.secret
	// NULL while the enrolment has not been confirmed, which is the state every
	// test about early activation is written around.
	if row.confirmedAt.IsZero() {
		dest[3] = nil
	} else {
		dest[3] = row.confirmedAt
	}
	dest[4] = row.lastUsedStep
	dest[5] = row.createdAt
	return nil
}

// recoveryCodeRows answers the narrow projection the spend reads.
type recoveryCodeRows struct {
	rows []recoveryCodeRow
	i    int
}

func (*recoveryCodeRows) Columns() []string { return []string{"id", "code_hash"} }

func (*recoveryCodeRows) Close() error { return nil }

func (r *recoveryCodeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.i]
	r.i++
	dest[0] = row.id
	dest[1] = row.hash
	return nil
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

	if result, err, handled := f.execTwoFactor(query, args); handled {
		return result, err
	}

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
				return f.result(1), nil
			}
		}
		return f.result(0), nil

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
				return f.result(1), nil
			}
		}
		return f.result(0), nil
	}
	return f.result(1), nil
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
	case strings.Contains(query, "FROM user_two_factor"):
		userID, tenant := text(args, 0), text(args, 1)
		for _, row := range f.twoFactor {
			if row.userID == userID && row.tenantID == tenant {
				return &twoFactorRows{rows: []twoFactorRow{row}}, nil
			}
		}
		return emptyRows{}, nil

	case strings.Contains(query, "FROM user_recovery_codes"):
		// The `used_at IS NULL` of the statement, applied only while the
		// statement says it. It is what keeps a spent code out of the candidate
		// list, and a fake that applied it regardless would keep that true after
		// the query stopped asking for it.
		onlyUnspent := strings.Contains(query, "used_at IS NULL")
		userID, tenant := text(args, 0), text(args, 1)
		var candidates []recoveryCodeRow
		for _, row := range f.recoveryCodes {
			if row.userID == userID && row.tenantID == tenant &&
				(!onlyUnspent || row.usedAt.IsZero()) {
				candidates = append(candidates, row)
			}
		}
		return &recoveryCodeRows{rows: candidates}, nil

	case strings.Contains(query, "FROM outbox"):
		var pending []events.Stored
		for _, e := range f.outbox {
			if !f.published[e.ID] {
				pending = append(pending, e)
			}
		}
		return &outboxRows{rows: pending}, nil

	case strings.Contains(query, "SELECT id, name, email FROM users"):
		tenant, _ := args[0].Value.(string)
		ids := make(map[string]struct{}, len(args)-1)
		for _, arg := range args[1:] {
			id, _ := arg.Value.(string)
			ids[id] = struct{}{}
		}
		var names []auth.User
		for _, u := range f.users {
			if u.TenantID != tenant {
				continue
			}
			if _, found := ids[u.ID]; found {
				names = append(names, u)
			}
		}
		return &nameRows{rows: names}, nil

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
type fakeResult struct {
	rows int64
	err  error
}

func (fakeResult) LastInsertId() (int64, error)   { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, r.err }

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

// nameRows answers the narrow projection used to label public authors.
type nameRows struct {
	rows []auth.User
	i    int
}

func (*nameRows) Columns() []string { return []string{"id", "name", "email"} }

func (*nameRows) Close() error { return nil }

func (r *nameRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	u := r.rows[r.i]
	r.i++
	dest[0] = u.ID
	if u.Name == "" {
		dest[1] = nil
	} else {
		dest[1] = u.Name
	}
	dest[2] = u.Email
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
