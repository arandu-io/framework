package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// ErrUserNotFound is returned when no row matches, so callers do not have to
// import database/sql to compare against sql.ErrNoRows.
var ErrUserNotFound = errors.New("auth: user not found")

// ErrEmailTaken is returned when the tenant already has that address.
var ErrEmailTaken = errors.New("auth: email already registered in this tenant")

// Pagination bounds for List. A request that asks for everything gets the
// maximum, never everything: an unbounded query is how one page load takes a
// production database down.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// UserRepo is the only door to the users table.
//
// Every method starts with g.Check: the Grant is required by the signature, and
// the check proves the grant was issued for this exact action.
//
// The SQL is written with "?" placeholders and with types every supported
// database shares, so the same statements run on SQLite and on PostgreSQL. The
// dialect rebinds the placeholders; nothing else needs translating.
type UserRepo struct {
	db *data.DB
}

// NewUserRepo returns a repository over an instrumented handle.
func NewUserRepo(db *data.DB) *UserRepo { return &UserRepo{db: db} }

// Repository conformance is asserted at compile time: if the contract changes,
// this line breaks before any caller does.
var _ data.Repository[User, string] = (*UserRepo)(nil)

const userColumns = `id, tenant_id, name, email, password, roles, verified_at, created_at`

// Find returns one user by id, scoped to the grant's tenant.
func (r *UserRepo) Find(ctx context.Context, g security.Grant, id string) (User, error) {
	if err := g.Check(ActionUserView); err != nil {
		return User{}, err
	}
	// The tenant comes from the Grant, never from the request.
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))
	return scanUser(row)
}

// FindByEmail returns one user by email, scoped to the grant's tenant.
//
// The address is normalized the same way on write and on read, which is what
// makes a plain UNIQUE index behave case-insensitively without a database
// specific collation.
func (r *UserRepo) FindByEmail(ctx context.Context, g security.Grant, email string) (User, error) {
	if err := g.Check(ActionUserView); err != nil {
		return User{}, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? AND tenant_id = ?`,
		NormalizeEmail(email), data.Tenant(g))
	return scanUser(row)
}

// Create inserts the user and returns it as stored.
//
// The id and the timestamp are generated here rather than by the database: a
// DEFAULT that produces a uuid is spelled differently in every engine, and
// generating them in Go is what keeps one schema working everywhere.
func (r *UserRepo) Create(ctx context.Context, g security.Grant, u User) (User, error) {
	if err := g.Check(ActionUserCreate); err != nil {
		return User{}, err
	}
	if u.Password == "" {
		return User{}, fmt.Errorf("auth: refusing to store a user without a password hash")
	}

	roles, err := json.Marshal(rolesOrEmpty(u.Roles))
	if err != nil {
		return User{}, err
	}
	if u.ID == "" {
		if u.ID, err = NewID(); err != nil {
			return User{}, err
		}
	}
	u.TenantID = data.Tenant(g)
	u.Email = NormalizeEmail(u.Email)
	u.CreatedAt = time.Now().UTC()

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.TenantID, nullString(u.Name), u.Email, u.Password, roles,
		nullTime(u.VerifiedAt), u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	return u, nil
}

// Update writes the mutable fields. The tenant is not one of them: moving a user
// between tenants is not an update, it is a migration.
func (r *UserRepo) Update(ctx context.Context, g security.Grant, u User) (User, error) {
	if err := g.Check(ActionUserUpdate); err != nil {
		return User{}, err
	}
	roles, err := json.Marshal(rolesOrEmpty(u.Roles))
	if err != nil {
		return User{}, err
	}
	u.Email = NormalizeEmail(u.Email)

	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET name = ?, email = ?, password = ?, roles = ?, verified_at = ?
		 WHERE id = ? AND tenant_id = ?`,
		nullString(u.Name), u.Email, u.Password, roles, nullTime(u.VerifiedAt),
		u.ID, data.Tenant(g))
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return User{}, err
	}
	if n == 0 {
		return User{}, ErrUserNotFound
	}
	u.TenantID = data.Tenant(g)
	return u, nil
}

// Confirm stamps the address as verified, and reports whether this call is what
// stamped it.
//
// It is a single conditional statement rather than a read followed by Update,
// and the difference is a duplicate welcome mail. A link sitting in an inbox is
// opened by the person and prefetched by whatever scans their mail, within the
// same second: two requests read an unverified row, two full-row updates
// succeed, and the service publishes auth.email.verified twice for one
// confirmation. `verified_at IS NULL` in the statement makes the database the
// referee, so exactly one caller is told it confirmed the address.
//
// It writes one column, which is the other half of the same problem. Update
// writes name, email, password and roles back from a snapshot taken before the
// transaction opened, so a confirmation that overlapped a password change put
// the old hash back -- and, once an address can be changed, put the old address
// back and marked it verified, undoing the binding the link exists to enforce.
//
// It does not read the row: the caller already has it, and asking twice would
// widen the window this closes.
func (r *UserRepo) Confirm(ctx context.Context, g security.Grant, id string, at time.Time) (bool, error) {
	if err := g.Check(ActionUserUpdate); err != nil {
		return false, err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET verified_at = ? WHERE id = ? AND tenant_id = ? AND verified_at IS NULL`,
		at.UTC(), id, data.Tenant(g))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		// Answering "yes, this call did it" without knowing would publish the
		// event twice; answering "no" would lose it. Every driver this framework
		// targets counts, so a driver that cannot is a wiring mistake and says so.
		return false, fmt.Errorf("auth: the driver cannot report how many rows the confirmation changed: %w", err)
	}
	return n > 0, nil
}

// SetPassword writes the password column of one user, and nothing else.
//
// It is Confirm's shape applied to the other column that is written on its own,
// and it exists for the same reason: Update writes name, email, roles and
// verified_at from whatever snapshot the caller happened to read, so any of them
// changing between that read and the write is silently put back. That is a lost
// update with three ways to hurt, and none of them fails or logs anything:
//
//   - an administrator grants a role while somebody is resetting their password,
//     and the reset takes it away again;
//   - the person clicks the verification link in the same minute, and the reset
//     un-verifies the address;
//   - once an address can be changed, the reset restores the old one -- together
//     with the verified stamp that belonged to it, which undoes the binding the
//     verification link exists to enforce.
//
// The read the caller did for the id is still outside the transaction, and does
// not need to be inside it: this statement names the row rather than describing
// its contents, so nothing it writes depends on what was read.
//
// A missing row is ErrUserNotFound rather than silence. A password reset that
// changed nothing and said it worked is somebody typing a new password and
// signing in with the old one.
func (r *UserRepo) SetPassword(ctx context.Context, g security.Grant, id, hash string) error {
	if err := g.Check(ActionUserUpdate); err != nil {
		return err
	}
	if hash == "" {
		// The same refusal Create makes, for the same reason: an empty column
		// here is an account whose password verification fails forever, and the
		// only way to reach it is a caller that stored a plain password by
		// mistake and lost it.
		return fmt.Errorf("auth: refusing to store a user without a password hash")
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET password = ? WHERE id = ? AND tenant_id = ?`,
		hash, id, data.Tenant(g))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Delete removes one user within the grant's tenant.
func (r *UserRepo) Delete(ctx context.Context, g security.Grant, id string) error {
	if err := g.Check(ActionUserDelete); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM users WHERE id = ? AND tenant_id = ?`, id, data.Tenant(g))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// sortableUser is the ordering allowlist. A sort field is a column name, and a
// column name interpolated from the request is injection through another door --
// so the only accepted values are the ones listed here.
var sortableUser = map[string]string{
	"":           "created_at",
	"email":      "email",
	"created_at": "created_at",
}

// List returns a page of users in the grant's tenant.
//
// Pagination is keyset based on (created_at, id): OFFSET grows more expensive
// with every page and skips rows when data changes underneath.
func (r *UserRepo) List(ctx context.Context, g security.Grant, q data.Query) ([]User, error) {
	if err := g.Check(ActionUserView); err != nil {
		return nil, err
	}
	column, ok := sortableUser[q.Sort]
	if !ok {
		return nil, fmt.Errorf("auth: sort field not allowed: %q", q.Sort)
	}
	limit := q.Limit
	switch {
	case limit <= 0:
		limit = defaultLimit
	case limit > maxLimit:
		limit = maxLimit
	}

	query := `SELECT ` + userColumns + ` FROM users WHERE tenant_id = ?`
	args := []any{data.Tenant(g)}
	if q.Cursor != "" {
		// Row values -- (a, b) > (c, d) -- are not portable, so the comparison is
		// written out. It is the same index scan, spelled for every engine.
		query += ` AND (created_at > (SELECT created_at FROM users WHERE id = ? AND tenant_id = ?)
		            OR (created_at = (SELECT created_at FROM users WHERE id = ? AND tenant_id = ?) AND id > ?))`
		args = append(args, q.Cursor, args[0], q.Cursor, args[0], q.Cursor)
	}
	query += ` ORDER BY ` + column + `, id LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// NamesByID returns the display name of each id, in one query.
//
// It exists because of what the alternative looks like on a comment thread:
// twenty comments, twenty lookups, and a page that is fine on a laptop and slow
// on the first article somebody actually discusses.
//
// Ids that do not exist are absent from the map rather than mapped to "". A
// caller can then tell "no such user" from "a user with no name", and the two
// deserve different words on a screen.
//
// The placeholder list is built from the count and never from the values, so
// this is a parameterised query however many ids arrive.
func (r *UserRepo) NamesByID(ctx context.Context, g security.Grant, ids []string) (map[string]string, error) {
	if err := g.Check(ActionUserView); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	if len(ids) > maxLimit {
		ids = ids[:maxLimit]
	}

	args := make([]any, 0, len(ids)+1)
	args = append(args, data.Tenant(g))
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, email FROM users WHERE tenant_id = ? AND id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string, len(ids))
	for rows.Next() {
		var (
			id    string
			name  sql.NullString
			email string
		)
		if err := rows.Scan(&id, &name, &email); err != nil {
			return nil, err
		}
		// The address is the fallback, and only its local part: an account
		// created before names existed still signs its comments with something
		// a person recognises, and publishing the whole address next to it
		// would be publishing an address nobody agreed to publish.
		display := name.String
		if display == "" {
			display, _, _ = strings.Cut(email, "@")
		}
		out[id] = display
	}
	return out, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves single-row and multi-row queries.
type rowScanner interface{ Scan(dest ...any) error }

// scanUser reads one row. Roles are stored as TEXT holding JSON: a Postgres
// array needs a driver specific type, and jsonb does not exist in SQLite.
func scanUser(row rowScanner) (User, error) {
	var (
		u        User
		roles    []byte
		name     sql.NullString
		verified sql.NullTime
	)
	err := row.Scan(&u.ID, &u.TenantID, &name, &u.Email, &u.Password, &roles,
		&verified, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	if len(roles) > 0 {
		if err := json.Unmarshal(roles, &u.Roles); err != nil {
			return User{}, fmt.Errorf("auth: unreadable roles for user %s: %w", u.ID, err)
		}
	}
	// Both columns arrived in a later migration, so every row written before it
	// has NULL in them. Read through sql.Null* rather than into the field: a
	// plain *string scan of a NULL is an error, and it would be an error on
	// every user that existed before verification did.
	u.Name = name.String
	u.VerifiedAt = verified.Time.UTC()
	return u, nil
}

// nullString and nullTime keep the empty value out of the column as NULL.
//
// An empty string and NULL both read back as "", so the distinction does not
// matter on the way out -- but a zero time.Time does not survive the round trip:
// it writes as 0001-01-01, which is a date, so `WHERE verified_at IS NOT NULL`
// answers true for every unverified user. That exact mistake shipped once in
// this repository's post listing.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// rolesOrEmpty keeps a nil slice out of the database: JSON 'null' and JSON '[]'
// read back differently, and only one of them means "no roles".
func rolesOrEmpty(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
}

// NormalizeEmail lowercases and trims the address.
//
// Addresses are case-insensitive in practice, and storing them normalized is what
// keeps a plain UNIQUE index correct on every engine -- rather than a functional
// index in Postgres and a collation in MySQL.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NewID returns a version 4 UUID as text.
//
// It forwards to data.NewID, which is where the one implementation lives: two
// id generators in one binary is two answers to "what does an id look like".
func NewID() (string, error) { return data.NewID() }

// isUniqueViolation recognizes a duplicate key across engines by message, which
// is ugly and is the price of not importing a driver into the core: SQLite says
// "UNIQUE constraint failed", Postgres says "duplicate key value violates unique
// constraint", MySQL says "Duplicate entry".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry")
}
