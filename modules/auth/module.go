// Package auth is the first first-party module and the canonical reference:
// every module the CLI generates has exactly this shape.
//
// Files of a module (vertical layout, not one directory per layer):
//
//	module.go       -> registration and routes
//	user.entity.go  -> the entity
//	user.policy.go  -> who may do what
//	user.repo.go    -> data access, requires a Grant
//	user.service.go -> business rules
//	user.request.go -> input types and Validate
package auth

import (
	"context"
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// TenantResolver decides which tenant a request belongs to.
//
// It is only consulted on login, which is the one moment where there is no
// session to ask: everywhere else the tenant comes from the Grant, and therefore
// from the session. That asymmetry is the point -- a tenant taken from the
// request body or from a header after login would defeat the isolation the whole
// policy layer is built on.
type TenantResolver func(r *http.Request) string

// FixedTenant is the resolver for a single-tenant application: every login
// belongs to the same tenant.
//
// This is not a "single-tenant mode". It is the same code path with a constant
// where the resolver would be, which is why an application that starts single
// and grows into multi changes one line and no queries.
func FixedTenant(id string) TenantResolver {
	return func(*http.Request) string { return id }
}

// Module registers the authentication routes.
type Module struct {
	svc    *Service
	tenant TenantResolver
}

// New returns the module. The service and the tenant resolver are built by the
// caller, because the wiring is explicit: if you want to know where UserRepo
// comes from, it is written in the application's main.
//
// A nil resolver means the empty tenant, which is what a single-tenant
// application that never set one ends up with -- consistent, and still isolated,
// because every row is written with that same value.
func New(svc *Service, tenant TenantResolver) *Module {
	if tenant == nil {
		tenant = FixedTenant("")
	}
	return &Module{svc: svc, tenant: tenant}
}

// Compile-time proof that the module honors the kernel contracts it claims.
var (
	_ kernel.Module     = (*Module)(nil)
	_ kernel.Migratable = (*Module)(nil)
	_ kernel.Health     = (*Module)(nil)
)

// Name is the module identifier.
func (m *Module) Name() string { return "auth" }

// Routes registers the module's routes.
//
// The sign-in screen is guarded, and it is the one route here that needs to be:
// without the guest guard it renders for somebody who already has a session,
// which reads to them as having been signed out. There is nothing to guard on
// the two POSTs -- signing in again is harmless, and signing out without a
// session is a no-op that ends where it should.
//
// The sign-in address is middleware.SignInPath and not a "/auth" group with a
// "/login" leaf, so that the address the guards redirect to and the address this
// module answers at are one string. Two spellings of one path can disagree, and
// the failure when they do is a guard that redirects to a 404 -- on the screen
// every application has, reachable only once somebody is signed out.
//
// The screens are named so that a URL can be built from a name instead of
// written out. The POST to the sign-in address is deliberately not: it shares
// the path with the GET, so a path built from "auth.login" is already where it
// posts, and a second name for one address is a choice nobody can make
// correctly.
func (m *Module) Routes(r *fhttp.Router) {
	r.Get(middleware.SignInPath, m.showLogin,
		middleware.RedirectIfAuthenticated(m.svc.session, "/")).Name("auth.login")
	r.Post(middleware.SignInPath, m.doLogin)
	r.Post("/auth/logout", m.doLogout).Name("auth.logout")
}

// Health reports whether the module can reach its storage. It feeds
// /_arandu/health, so a database that went away turns into a failing probe
// rather than a stream of 500s.
func (m *Module) Health(ctx context.Context) error {
	return m.svc.repo.db.PingContext(ctx)
}

// Migrations declares the schema this module owns.
//
// They are returned in the order their names sort in, which is the order they
// apply in: the name carries the order, and nothing else decides it.
func (m *Module) Migrations() []kernel.Migration {
	return []kernel.Migration{createUsers{}, addNameAndVerificationToUsers{}}
}

// Both migrations are reversible, and the assertion is here rather than
// discovered at rollback: the Migrator tests for Down with a type assertion, so
// a Down with the wrong signature would leave a rollback that silently does
// nothing.
var (
	_ migrations.ReversibleMigration = createUsers{}
	_ migrations.ReversibleMigration = addNameAndVerificationToUsers{}
)

// createUsers is the users table and the index the listing reads it by.
type createUsers struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createUsers) GetName() string { return "20260729_0001_create_users" }

// Up creates the table and its index.
//
// Every type here spells the same in SQLite, PostgreSQL and MySQL, which is what
// lets one project develop on a file and deploy on Postgres without a second
// schema. The three things that would have broken that, and what replaced them:
//
//   - uuid columns are TEXT, and the id is generated by the application;
//   - roles are TEXT holding JSON, not jsonb and not text[];
//   - created_at has no database default, the value comes from Go.
//
// The email is stored lowercased by the repository, so a plain UNIQUE index is
// enough and no database-specific case-insensitive collation is needed.
func (createUsers) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, "users", func(table *schema.Blueprint) {
		table.String("id").Primary()
		// String rather than Text for the keyed columns, and the Blueprint is
		// what makes that a name rather than a rule to remember: MySQL refuses
		// TEXT in a key without a prefix length, and the grammar knows it.
		table.String("tenant_id")
		table.String("email")
		table.Text("password")
		table.Text("roles")
		table.Timestamp("created_at")

		table.Unique([]string{"tenant_id", "email"})
		table.Index([]string{"tenant_id", "created_at", "id"}, "users_tenant_created_idx")
	})
}

// Down drops the table, which takes its index with it.
func (createUsers) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, "users")
}

// addNameAndVerificationToUsers adds the display name and the verification
// timestamp.
//
// A second migration and not an edit to the first. The first one has run in
// every database this module has ever touched, and a migration is identified by
// its name: changing what an applied name means leaves the column missing
// everywhere it already ran, and nothing says so.
type addNameAndVerificationToUsers struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (addNameAndVerificationToUsers) GetName() string {
	return "20260809_0002_add_name_and_verification_to_users"
}

// Up adds the two columns.
//
// Both are nullable: during a rollout the previous binary is still inserting
// rows without them, and a NOT NULL column with no default fails every one of
// those inserts.
//
// Two statements rather than one with two clauses: MySQL accepts
// `ADD COLUMN a, ADD COLUMN b`, SQLite does not.
func (addNameAndVerificationToUsers) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Table(ctx, "users", func(table *schema.Blueprint) {
		// Both nullable, and that is the rollout rule: a NOT NULL column added
		// to a table that has rows fails on every row already there, and during
		// a rollout the previous binary does not fill it in.
		table.String("name").Nullable()
		table.Timestamp("verified_at").Nullable()
	})
}

// Down drops the two columns, one statement each for the reason Up gives.
func (addNameAndVerificationToUsers) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Table(ctx, "users", func(table *schema.Blueprint) {
		table.DropColumn("name", "verified_at")
	})
}
