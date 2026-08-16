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

	// The tenant comes from the Grant the policy issued, never from the request.
	// The request has no field for it, and this is why.
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

// TestCreateUserRequiresThePolicy is the mandatory policy at the write end: no
// repository is reachable without a Grant, and the Grant only exists because the
// policy issued one. A member creating a user is the denial that must cost
// nothing.
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

// A password reset used to read the row, set one field, and write the whole row
// back -- with the read outside the transaction. Everything that changed in
// between was reverted from a stale snapshot, and nothing failed or logged:
//
//   - an administrator grants a role during the reset, and the reset takes it
//     away again;
//   - the person clicks the verification link in the same minute, and the reset
//     un-verifies the address;
//   - once an address can be changed, the reset puts the old one back together
//     with its verified stamp, undoing the binding the verification link exists
//     to enforce.
//
// The statement is the proof, because it is what a concurrent writer collides
// with: a write that names one column cannot revert a column it does not name.
// It is the fix MarkVerified was given in UserRepo.Confirm, applied to the other
// column that is written on its own.
func TestReplacingAPasswordDoesNotWriteBackTheRestOfTheRow(t *testing.T) {
	service, db := serviceOverFakeDB(t)
	db.seedUser(auth.User{
		ID: "u-1", TenantID: tenant, Name: "Ada Lovelace", Email: "ada@example.com",
		Password: "the-old-hash", Roles: []string{"admin"},
	})

	if _, err := service.SetPassword(context.Background(), tenant, "ada@example.com", "a-long-enough-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	var wrote bool
	for _, stmt := range db.statements() {
		if !strings.HasPrefix(stmt, "UPDATE users") {
			continue
		}
		wrote = true
		for _, column := range []string{"name =", "email =", "roles =", "verified_at ="} {
			if strings.Contains(stmt, column) {
				t.Errorf("the reset also writes %s, from a snapshot read before the transaction opened: %s", column, stmt)
			}
		}
		if !strings.Contains(stmt, "tenant_id = ?") {
			t.Errorf("the reset is not scoped by tenant, so it names a row of whichever customer holds that id (RULE 14): %s", stmt)
		}
	}
	if !wrote {
		t.Fatalf("no password was written at all:\n%v", db.statements())
	}
}

// The account the notice is about, as it is at commit rather than as it was
// before the hash was computed.
//
// EventPasswordReset is what a "your password was changed" mail is sent from, so
// a payload built from the snapshot taken a hundred milliseconds earlier -- an
// argon2 hash is that long -- sends that mail to the address the account had
// before the change. This is the one case where being slightly stale is the
// whole problem.
//
// The order of the statements is the proof: the row that becomes the payload is
// read after the write, inside the same transaction.
func TestAPasswordResetPublishesTheAccountAsItIsAfterTheWrite(t *testing.T) {
	service, db := serviceOverFakeDB(t)
	db.seedUser(auth.User{
		ID: "u-1", TenantID: tenant, Name: "Ada Lovelace", Email: "ada@example.com",
		Password: "the-old-hash", Roles: []string{"admin"},
	})

	updated, err := service.SetPassword(context.Background(), tenant, "ada@example.com", "a-long-enough-password")
	if err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if updated.ID != "u-1" || updated.Email != "ada@example.com" || updated.Name != "Ada Lovelace" {
		t.Errorf("the caller was handed %+v, and a seeder prints these back to an operator", updated)
	}

	wrote, read, published := -1, -1, -1
	for i, stmt := range db.statements() {
		switch {
		case strings.HasPrefix(stmt, "UPDATE users"):
			wrote = i
		case strings.Contains(stmt, "FROM users") && wrote >= 0 && read < 0:
			read = i
		case strings.Contains(stmt, "INSERT INTO outbox"):
			published = i
		}
	}
	if wrote < 0 || published < 0 {
		t.Fatalf("the reset did not write and publish:\n%v", db.statements())
	}
	if read < 0 || read > published {
		t.Fatalf("the event was built from the row as it was read before the write, so a notice goes to the address the account no longer has:\n%v", db.statements())
	}
}

// A reset that changed nothing and reported success is somebody typing a new
// password and then signing in with the old one.
func TestReplacingThePasswordOfAnAccountThatIsNotThereIsAnError(t *testing.T) {
	service, _ := serviceOverFakeDB(t)

	if _, err := service.SetPassword(context.Background(), tenant, "nobody@example.com", "a-long-enough-password"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}
