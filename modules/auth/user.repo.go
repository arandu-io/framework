package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// ErrUserNotFound is returned when no row matches, so callers do not have to
// import database/sql to compare against sql.ErrNoRows.
var ErrUserNotFound = errors.New("auth: user not found")

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
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND tenant_id = $2`,
		id, data.Tenant(g))
	return scanUser(row)
}

// FindByEmail returns one user by email, scoped to the grant's tenant.
func (r *UserRepo) FindByEmail(ctx context.Context, g security.Grant, email string) (User, error) {
	if err := g.Check(ActionUserView); err != nil {
		return User{}, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1 AND tenant_id = $2`,
		email, data.Tenant(g))
	return scanUser(row)
}

// Create inserts the user and returns it with the id and timestamp the database
// assigned.
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
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO users (tenant_id, email, password, roles)
		      VALUES ($1, $2, $3, $4)
		   RETURNING `+userColumns,
		data.Tenant(g), u.Email, u.Password, roles)
	return scanUser(row)
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
	row := r.db.QueryRowContext(ctx,
		`UPDATE users
		    SET email = $1, password = $2, roles = $3
		  WHERE id = $4 AND tenant_id = $5
		RETURNING `+userColumns,
		u.Email, u.Password, roles, u.ID, data.Tenant(g))
	return scanUser(row)
}

// Delete removes one user within the grant's tenant.
func (r *UserRepo) Delete(ctx context.Context, g security.Grant, id string) error {
	if err := g.Check(ActionUserDelete); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM users WHERE id = $1 AND tenant_id = $2`, id, data.Tenant(g))
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

	query := `SELECT ` + userColumns + ` FROM users WHERE tenant_id = $1`
	args := []any{data.Tenant(g)}
	if q.Cursor != "" {
		query += ` AND (created_at, id) > (SELECT created_at, id FROM users WHERE id = $2 AND tenant_id = $1)`
		args = append(args, q.Cursor)
	}
	query += ` ORDER BY ` + column + `, id LIMIT $` + strconv.Itoa(len(args)+1)
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

// scanUser reads one row. Roles are stored as jsonb and scanned as bytes on
// purpose: a Postgres text[] needs a driver specific array type, and the core
// has no driver dependency.
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

// rolesOrEmpty keeps a nil slice out of the database: jsonb 'null' and jsonb
// '[]' read back differently, and only one of them means "no roles".
func rolesOrEmpty(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
}
