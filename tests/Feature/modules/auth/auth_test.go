package feature

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/migrations"
)

// The tests below need no database: every repository method checks the Grant
// before touching the handle, which is exactly the property under test.
func repoWithoutDB() *auth.UserRepo { return auth.NewUserRepo(nil) }

// TestEveryRepositoryMethodRequiresItsGrant is the runtime counterpart of the
// compile-time proof in the data package: the zero Grant -- the only one callers
// can build -- never gets through, and a grant for one action does not open
// another.
func TestEveryRepositoryMethodRequiresItsGrant(t *testing.T) {
	repo := repoWithoutDB()
	ctx := context.Background()
	var zero security.Grant

	calls := map[string]func(g security.Grant) error{
		"Find": func(g security.Grant) error {
			_, err := repo.Find(ctx, g, "id")
			return err
		},
		"FindByEmail": func(g security.Grant) error {
			_, err := repo.FindByEmail(ctx, g, "a@b.co")
			return err
		},
		"List": func(g security.Grant) error {
			_, err := repo.List(ctx, g, data.Query{})
			return err
		},
		"Create": func(g security.Grant) error {
			_, err := repo.Create(ctx, g, auth.User{})
			return err
		},
		"Update": func(g security.Grant) error {
			_, err := repo.Update(ctx, g, auth.User{})
			return err
		},
		"Delete": func(g security.Grant) error {
			return repo.Delete(ctx, g, "id")
		},
	}

	for name, call := range calls {
		t.Run(name+" with no grant", func(t *testing.T) {
			if err := call(zero); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("error = %v, want ErrForbidden", err)
			}
		})
		t.Run(name+" with a grant for another action", func(t *testing.T) {
			wrong := security.SystemGrant("some.other.action", "t1")
			if err := call(wrong); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("error = %v, want ErrForbidden", err)
			}
		})
	}
}

// TestListRejectsSortOutsideTheAllowlist: a sort field is a column name, and a
// column name taken from the request is injection through another door.
func TestListRejectsSortOutsideTheAllowlist(t *testing.T) {
	repo := repoWithoutDB()
	g := security.SystemGrant(auth.ActionUserView, "t1")

	_, err := repo.List(context.Background(), g, data.Query{Sort: "password; DROP TABLE users"})

	if err == nil {
		t.Fatal("an arbitrary sort field was accepted")
	}
	if strings.Contains(err.Error(), "DROP TABLE") && !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error = %v", err)
	}
}

func TestPolicyIsolatesTenants(t *testing.T) {
	policy := auth.UserPolicy{}
	admin := security.Subject{ID: "admin-1", Tenant: "tenant-a", Roles: []string{"admin"}}
	otherTenant := auth.User{ID: "u-9", TenantID: "tenant-b"}

	// An administrator is still an administrator of their own tenant only.
	err := policy.Can(context.Background(), admin, auth.ActionUserView, otherTenant)

	if err == nil {
		t.Fatal("an admin reached a resource in another tenant")
	}
	if !strings.Contains(err.Error(), "another tenant") {
		t.Errorf("error = %v", err)
	}
}

func TestPolicyDecisions(t *testing.T) {
	policy := auth.UserPolicy{}
	ctx := context.Background()
	admin := security.Subject{ID: "admin-1", Tenant: "t1", Roles: []string{"admin"}}
	member := security.Subject{ID: "u-1", Tenant: "t1"}
	self := auth.User{ID: "u-1", TenantID: "t1"}
	other := auth.User{ID: "u-2", TenantID: "t1"}

	cases := []struct {
		name    string
		subject security.Subject
		action  security.Action
		target  auth.User
		allowed bool
	}{
		{"member views themselves", member, auth.ActionUserView, self, true},
		{"member views someone else", member, auth.ActionUserView, other, false},
		{"member updates themselves", member, auth.ActionUserUpdate, self, true},
		{"member creates a user", member, auth.ActionUserCreate, auth.User{}, false},
		{"member deletes someone", member, auth.ActionUserDelete, other, false},
		{"admin views someone", admin, auth.ActionUserView, other, true},
		{"admin creates a user", admin, auth.ActionUserCreate, auth.User{}, true},
		{"admin deletes someone", admin, auth.ActionUserDelete, other, true},
		{"unknown action is denied", admin, "auth.user.impersonate", other, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := policy.Can(ctx, c.subject, c.action, c.target)
			if c.allowed && err != nil {
				t.Fatalf("denied: %v", err)
			}
			if !c.allowed && err == nil {
				t.Fatal("allowed, want denied")
			}
		})
	}
}

// TestAuthorizeProducesAUsableGrant walks the whole mandatory path once, which is
// what the generated modules will copy.
func TestAuthorizeProducesAUsableGrant(t *testing.T) {
	admin := security.Subject{ID: "admin-1", Tenant: "t1", Roles: []string{"admin"}}

	g, err := security.Authorize(context.Background(), auth.UserPolicy{}, admin, auth.ActionUserCreate, auth.User{})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	if err := g.Check(auth.ActionUserCreate); err != nil {
		t.Fatalf("the issued grant does not pass its own action: %v", err)
	}
	if data.Tenant(g) != "t1" {
		t.Fatalf("tenant from grant = %q, want t1", data.Tenant(g))
	}
}

// TestPasswordHashNeverLeaves is the reason User implements MarshalJSON and
// LogValue: a single dump or log line would otherwise publish the hash on the
// debug page.
func TestPasswordHashNeverLeaves(t *testing.T) {
	u := auth.User{ID: "u-1", TenantID: "t1", Email: "a@b.co", Password: "$argon2id$secret"}

	encoded, err := u.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if strings.Contains(string(encoded), "argon2id") {
		t.Fatalf("the hash reached JSON: %s", encoded)
	}

	logged := u.LogValue().String()
	if strings.Contains(logged, "argon2id") {
		t.Fatalf("the hash reached the log: %s", logged)
	}
	if !strings.Contains(logged, "u-1") {
		t.Fatalf("the log value must still identify the user: %s", logged)
	}
}

// TestMarshalJSONEscapesProperly: the first version of this method concatenated
// strings, so a quote in the email produced invalid JSON.
func TestMarshalJSONEscapesProperly(t *testing.T) {
	u := auth.User{ID: "u-1", Email: `we"ird@example.com`}

	encoded, err := u.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	if !strings.Contains(string(encoded), `we\"ird`) {
		t.Fatalf("the quote was not escaped: %s", encoded)
	}
}

func TestCreateUserRequestValidation(t *testing.T) {
	cases := map[string]struct {
		request auth.CreateUserRequest
		fields  []string
	}{
		"valid": {
			request: auth.CreateUserRequest{Email: "a@b.co", Password: "long-enough-password"},
		},
		"missing email": {
			request: auth.CreateUserRequest{Password: "long-enough-password"},
			fields:  []string{"email"},
		},
		"malformed email": {
			request: auth.CreateUserRequest{Email: "not-an-email", Password: "long-enough-password"},
			fields:  []string{"email"},
		},
		"short password": {
			request: auth.CreateUserRequest{Email: "a@b.co", Password: "short"},
			fields:  []string{"password"},
		},
		"password too long": {
			request: auth.CreateUserRequest{Email: "a@b.co", Password: strings.Repeat("x", 129)},
			fields:  []string{"password"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			errs := c.request.Validate()
			if len(c.fields) == 0 {
				if errs.Any() {
					t.Fatalf("valid input was rejected: %v", errs)
				}
				return
			}
			for _, f := range c.fields {
				if len(errs[f]) == 0 {
					t.Errorf("field %q was not reported: %v", f, errs)
				}
			}
		})
	}
}

func TestSubjectOfCarriesTenantAndRoles(t *testing.T) {
	u := auth.User{ID: "u-1", TenantID: "t1", Roles: []string{"admin"}}

	s := auth.SubjectOf(u)

	if s.ID != "u-1" || s.Tenant != "t1" || !s.HasRole("admin") {
		t.Fatalf("subject = %+v", s)
	}
}

func TestModuleRegistersItsRoutes(t *testing.T) {
	m := auth.New(auth.NewService(repoWithoutDB(), nil, nil), nil)

	if m.Name() != "auth" {
		t.Fatalf("Name = %q, want auth", m.Name())
	}

	declared := m.Migrations()
	if len(declared) == 0 {
		t.Fatal("the module declares no schema")
	}

	// The assertion is the intent, not one engine's spelling of it. The schema
	// is written with the Blueprint now, and the Blueprint emits a unique index
	// where the hand-written DDL wrote a table constraint -- the same guarantee,
	// in the words each engine uses.
	//
	// What has to stay true is that the tenant leads: an index on (email,
	// tenant_id) would also be unique per tenant and would be useless for every
	// lookup this module makes, and one on (email) alone would make an address
	// unique across every customer in the system.
	first := strings.ToLower(strings.Join(statementsOf(t, declared[0]), "\n"))
	if !strings.Contains(first, "unique") {
		t.Error("the schema declares no unique constraint at all")
	}
	if !strings.Contains(first, `"tenant_id", "email"`) {
		t.Error("the schema must keep emails unique per tenant, not globally, and with the tenant leading")
	}
	if strings.Contains(first, "text[]") {
		t.Error("roles must be jsonb: a Postgres array needs a driver specific type to scan")
	}

	// Every later migration is nullable or has a default: a rollout runs the
	// migration while the previous binary is still inserting rows that do not
	// know about the column.
	for _, later := range declared[1:] {
		for _, statement := range statementsOf(t, later) {
			if strings.Contains(statement, "ADD COLUMN") &&
				strings.Contains(statement, "NOT NULL") && !strings.Contains(statement, "DEFAULT") {
				t.Errorf("%s adds a NOT NULL column with no default: every insert from the previous binary fails during the rollout", later.GetName())
			}
		}

		down, err := migrations.DownStatements(context.Background(), later)
		if err != nil {
			t.Fatalf("%s: %v", later.GetName(), err)
		}
		if len(down) == 0 {
			t.Errorf("%s cannot be rolled back", later.GetName())
		}
	}

	// The names are unique and sorted, because the name is the whole of the
	// ordering rule and the repository records by it. A duplicate is a
	// migration recorded as applied by another one, and it never runs.
	seen := map[string]bool{}
	previous := ""
	for _, declaration := range declared {
		name := declaration.GetName()
		if seen[name] {
			t.Errorf("two migrations share the name %s", name)
		}
		if name <= previous {
			t.Errorf("%s is declared after %s but sorts before it, and the sort is what decides", name, previous)
		}
		seen[name] = true
		previous = name
	}
}

// statementsOf is the SQL a migration's Up would send, read the way the
// migrator reads it.
func statementsOf(t *testing.T, m migrations.Migration) []string {
	t.Helper()

	up, err := migrations.UpStatements(context.Background(), m)
	if err != nil {
		t.Fatalf("%s: %v", m.GetName(), err)
	}
	return up
}

// TestTenantComesFromTheApplication: a tenant chosen by the request is a tenant
// chosen by the attacker. The resolver is the only source on login, and after
// login the tenant lives in the Grant.
func TestTenantComesFromTheApplication(t *testing.T) {
	resolver := auth.FixedTenant("tenant-from-config")

	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.Header.Set("X-Tenant", "tenant-from-attacker")

	if got := resolver(r); got != "tenant-from-config" {
		t.Fatalf("resolved tenant = %q, want the one the application configured", got)
	}
}
