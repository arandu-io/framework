package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// serviceOverFakeDB returns the service wired the way `aru make:module` wires
// it, over a handle that records statements instead of running them.
func serviceOverFakeDB(t *testing.T) (*auth.Service, *fakeDB) {
	t.Helper()

	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	return auth.NewService(auth.NewUserRepo(data.Wrap(db, data.DialectSQLite)), nil, nil), state
}

// TestCreateUserWalksTheCanonicalPath is the test the doc comment promised and
// did not have.
//
// CreateUser is described as "the full path: validate, Authorize, Grant,
// Repository", and it is what every generated module copies. Untested, the
// example that teaches the mandatory path was the one place nothing checked the
// path was taken.
func TestCreateUserWalksTheCanonicalPath(t *testing.T) {
	service, db := serviceOverFakeDB(t)
	admin := security.Subject{ID: "admin-1", Tenant: "t1", Roles: []string{"admin"}}

	created, err := service.CreateUser(context.Background(), admin, auth.CreateUserRequest{
		Email:    "New.User@Example.COM",
		Password: "long-enough-password",
		Roles:    []string{"member"},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// The tenant comes from the Grant the policy issued, never from the request
	// (RULE 14). The request has no field for it, and this is why.
	if created.TenantID != "t1" {
		t.Errorf("TenantID = %q, want the actor's tenant", created.TenantID)
	}
	if created.Email != "new.user@example.com" {
		t.Errorf("Email = %q, want it normalized: the UNIQUE index is what makes it case-insensitive", created.Email)
	}
	if created.ID == "" {
		t.Error("the stored user has no id")
	}

	// The password is hashed before it reaches the repository, and the hash is
	// the one that verifies. Storing the plain text is the failure this whole
	// module exists to prevent.
	if created.Password == "long-enough-password" {
		t.Fatal("the password reached the repository in plain text")
	}
	if err := security.VerifyPassword("long-enough-password", created.Password); err != nil {
		t.Fatalf("the stored hash does not verify the password it was made from: %v", err)
	}

	args, ok := db.argsOf("INSERT INTO users")
	if !ok {
		t.Fatalf("no INSERT reached the database, statements were %v", db.statements())
	}
	for _, a := range args {
		if s, isString := a.Value.(string); isString && s == "long-enough-password" {
			t.Fatal("the plain password was sent to the database")
		}
	}
}

// TestCreateUserRefusesBeforeAuthorizing pins the order of the first two steps.
//
// Validation runs before the policy, and the difference is visible exactly when
// both would fail: a malformed form submitted by somebody who is also not
// allowed has to come back as "this email is not an email", not as "forbidden".
// The second answer sends the user to argue about permissions over a typo.
//
// Nothing reaches the database either way, which is what keeps a malformed
// address out of the UNIQUE index and out of the audit trail.
func TestCreateUserRefusesBeforeAuthorizing(t *testing.T) {
	for name, actor := range map[string]security.Subject{
		"allowed": {ID: "admin-1", Tenant: "t1", Roles: []string{"admin"}},
		"denied":  {ID: "u-1", Tenant: "t1"},
	} {
		t.Run(name, func(t *testing.T) {
			service, db := serviceOverFakeDB(t)

			_, err := service.CreateUser(context.Background(), actor, auth.CreateUserRequest{
				Email:    "not-an-email",
				Password: "short",
			})

			var errs validation.Errors
			if !errors.As(err, &errs) {
				t.Fatalf("error = %v, want validation errors naming the fields", err)
			}
			if len(errs["email"]) == 0 || len(errs["password"]) == 0 {
				t.Errorf("both fields must be reported at once: %v", errs)
			}
			if got := db.statements(); len(got) != 0 {
				t.Fatalf("an invalid request reached the database: %v", got)
			}
		})
	}
}

// TestCreateUserRequiresThePolicy is RULE 17 at the write end: no repository is
// reachable without a Grant, and the Grant only exists because the policy issued
// one. A member creating a user is the denial that must cost nothing.
func TestCreateUserRequiresThePolicy(t *testing.T) {
	service, db := serviceOverFakeDB(t)
	member := security.Subject{ID: "u-1", Tenant: "t1"}

	_, err := service.CreateUser(context.Background(), member, auth.CreateUserRequest{
		Email:    "new.user@example.com",
		Password: "long-enough-password",
	})

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if got := db.statements(); len(got) != 0 {
		t.Fatalf("a denied request still reached the database: %v", got)
	}
}

// TestCreateUserCannotReachAnotherTenant: the candidate is built with the
// actor's tenant, so there is no field an attacker could set to place a user
// somewhere else. This asserts the property rather than the absence of a field,
// because a field is exactly what somebody adds later.
func TestCreateUserCannotReachAnotherTenant(t *testing.T) {
	service, db := serviceOverFakeDB(t)
	admin := security.Subject{ID: "admin-1", Tenant: "tenant-a", Roles: []string{"admin"}}

	created, err := service.CreateUser(context.Background(), admin, auth.CreateUserRequest{
		Email:    "victim@example.com",
		Password: "long-enough-password",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.TenantID != "tenant-a" {
		t.Fatalf("TenantID = %q, want tenant-a", created.TenantID)
	}

	args, ok := db.argsOf("INSERT INTO users")
	if !ok {
		t.Fatal("no INSERT reached the database")
	}
	var tenants []string
	for _, a := range args {
		if s, isString := a.Value.(string); isString && strings.HasPrefix(s, "tenant-") {
			tenants = append(tenants, s)
		}
	}
	if len(tenants) != 1 || tenants[0] != "tenant-a" {
		t.Fatalf("the statement carried tenants %v, want exactly [tenant-a]", tenants)
	}
}
