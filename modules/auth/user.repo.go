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
// the check proves the grant was issued for this exact action. Phase 2 generates
// these bodies from queries.sql with sqlc; the signature and the check do not
// change when it does.
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

const userColumns = `id, tenant_id, email, password, roles, created_at`

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
		`INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.TenantID, u.Email, u.Password, roles, u.CreatedAt)
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
		`UPDATE users SET email = ?, password = ?, roles = ? WHERE id = ? AND tenant_id = ?`,
		u.Email, u.Password, roles, u.ID, data.Tenant(g))
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return User{}, ErrUserNotFound
	}
	return u, nil
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
	if n, err := res.RowsAffected(); err == nil && n == 0 {
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

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves single-row and multi-row queries.
type rowScanner interface{ Scan(dest ...any) error }

// scanUser reads one row. Roles are stored as TEXT holding JSON: a Postgres
// array needs a driver specific type, and jsonb does not exist in SQLite.
func scanUser(row rowScanner) (User, error) {
	var (
		u     User
		roles []byte
	)
	err := row.Scan(&u.ID, &u.TenantID, &u.Email, &u.Password, &roles, &u.CreatedAt)
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
	return u, nil
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
