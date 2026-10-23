package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/auth/account"
	"github.com/arandu-io/hesape/auth/account/accounttest"
)

// Account runs the contract of hesape/auth/account against what the
// application hands it: open returns, once per subtest, a fresh database with
// the application's users table, the outbox and the second factor's tables,
// the application's users query and its user policy.
//
// It is accounttest.Run under the name the other kits share, so an
// application's tests/Conformance calls every kit the same way.
func Account(t *testing.T, open func(t *testing.T) accounttest.Fixture) {
	t.Helper()
	accounttest.Run(t, open)
}

// ModelFixture is the application's half of the account rules over one fresh
// database: what Account is handed, and what the application's user model
// adds to it.
type ModelFixture struct {
	accounttest.Fixture

	// Subject reads the session subject the application builds from one of
	// its accounts. Nil uses the account's own Subject method, which the
	// skeleton's user model has.
	Subject func(account.Account) auth.Subject

	// Role is a role the application grants, and GrantRole writes it on an
	// account before the account is saved. Both empty, the roles checks do
	// not run: an application whose roles live elsewhere has no column to
	// check.
	Role      string
	GrantRole func(a account.Account, role string) error

	// RolesColumn is the users column the roles are stored in, as a JSON
	// array. Empty is "roles".
	RolesColumn string
}

// ModelPersistence checks the application's half of the account rules:
//
//   - the account the rules read back is the model the users query built, of
//     the same type the rules wrote;
//   - a role written by Draft.Fill reads back as the one role, the session
//     subject carries it, and the column holds the JSON array -- an empty one
//     for an account registered with none;
//   - a registration whose Prepare hook grants a role is refused by the user
//     policy, and nothing is stored;
//   - the user policy refuses a public-name read from a subject with no
//     tenant;
//   - the model, its session subject and its JSON agree on the verification:
//     verified_at appears in the JSON exactly when the address is verified,
//     and the password and its hash appear in it never.
//
// open is called once per subtest.
func ModelPersistence(t *testing.T, open func(t *testing.T) ModelFixture) {
	t.Helper()
	m := modelKit{}
	t.Run("TheAccountReadBackIsTheModelTheQueryBuilt", func(t *testing.T) { m.readBack(t, open(t)) })
	t.Run("RolesAreOneValueInGoAndJSONInTheColumn", func(t *testing.T) { m.roles(t, open(t)) })
	t.Run("ARegistrationCannotGrantItselfARole", func(t *testing.T) { m.selfGrant(t, open(t)) })
	t.Run("APublicNameReadWithoutAReaderIsRefused", func(t *testing.T) { m.publicNames(t, open(t)) })
	t.Run("TheModelItsSubjectAndItsJSONAgreeOnTheVerification", func(t *testing.T) { m.verification(t, open(t)) })
}

type modelKit struct{}

const kitPassword = "a-long-enough-password"

func users(t *testing.T, f ModelFixture, hooks account.Hooks) *account.Users {
	t.Helper()
	u, err := account.NewUsers(account.Config{DB: f.DB, Query: f.Query, Policy: f.Policy, Hooks: hooks})
	if err != nil {
		t.Fatalf("building the account rules: %v", err)
	}
	return u
}

func subjectOf(t *testing.T, f ModelFixture, a account.Account) auth.Subject {
	t.Helper()
	s, err := subjectFrom(f.Subject, a)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// subjectFrom reads the session subject of an account: through the function a
// fixture gave, or the account's own Subject method.
func subjectFrom(custom func(account.Account) auth.Subject, a account.Account) (auth.Subject, error) {
	if custom != nil {
		return custom(a), nil
	}
	if s, ok := a.(interface{ Subject() auth.Subject }); ok {
		return s.Subject(), nil
	}
	return auth.Subject{}, fmt.Errorf("%T has no Subject method; give the fixture a Subject function", a)
}

func (modelKit) readBack(t *testing.T, f ModelFixture) {
	ctx := context.Background()
	u := users(t, f, account.Hooks{})
	written, err := u.Provision(ctx, "tenant-a", account.Draft{Name: "Ana", Email: "ana@example.test", Password: kitPassword, Verified: true})
	if err != nil {
		t.Fatalf("provisioning an account: %v", err)
	}
	read, err := u.Lookup(ctx, "tenant-a", "ANA@example.test")
	if err != nil {
		t.Fatalf("reading the account back: %v", err)
	}
	if reflect.TypeOf(read) != reflect.TypeOf(written) {
		t.Fatalf("the rules read back a %T and wrote a %T: the users query builds another model", read, written)
	}
	if read.AccountID() != written.AccountID() || read.AccountTenant() != "tenant-a" || read.AccountEmail() != "ana@example.test" || read.AccountName() != "Ana" {
		t.Fatalf("read back id=%q tenant=%q email=%q name=%q, want the account written", read.AccountID(), read.AccountTenant(), read.AccountEmail(), read.AccountName())
	}
	if _, err := u.Lookup(ctx, "tenant-a", "nobody@example.test"); !errors.Is(err, account.ErrUserNotFound) {
		t.Fatalf("looking up an unknown address answered %v, want account.ErrUserNotFound", err)
	}
}

func (modelKit) roles(t *testing.T, f ModelFixture) {
	if f.Role == "" && f.GrantRole == nil {
		t.Log("the fixture declares no role: the application keeps its roles outside the users table")
		return
	}
	if f.Role == "" || f.GrantRole == nil {
		t.Fatal("ModelFixture needs both Role and GrantRole")
	}
	ctx := context.Background()
	u := users(t, f, account.Hooks{})
	admin, err := u.Provision(ctx, "tenant-a", account.Draft{
		Email: "root@example.test", Password: kitPassword, Verified: true,
		Fill: func(a account.Account) error { return f.GrantRole(a, f.Role) },
	})
	if err != nil {
		t.Fatalf("provisioning an account with the role %s: %v", f.Role, err)
	}
	read, err := u.Lookup(ctx, "tenant-a", "root@example.test")
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if s := subjectOf(t, f, read); !s.HasRole(f.Role) || len(s.Roles) != 1 {
		t.Fatalf("the session subject read back has roles %v, want [%s]", s.Roles, f.Role)
	}
	if got := storedRoles(t, f, admin.AccountID()); len(got) != 1 || got[0] != f.Role {
		t.Fatalf("the roles column holds %v, want [%s]", got, f.Role)
	}

	member, err := u.Register(ctx, "tenant-a", account.Registration{Name: "Ana", Email: "ana@example.test", Password: kitPassword})
	if err != nil {
		t.Fatalf("registering a member: %v", err)
	}
	if got := storedRoles(t, f, member.AccountID()); got == nil || len(got) != 0 {
		t.Fatalf("a member's roles column holds %v, want the empty array", got)
	}
}

// storedRoles reads the roles column as the JSON array it must hold. A column
// that is NULL or not an array answers nil.
func storedRoles(t *testing.T, f ModelFixture, id string) []string {
	t.Helper()
	column := f.RolesColumn
	if column == "" {
		column = "roles"
	}
	var stored any
	if err := f.DB.QueryRowContext(context.Background(), `SELECT `+column+` FROM users WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("reading users.%s: %v", column, err)
	}
	var text string
	switch v := stored.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		t.Fatalf("users.%s holds a %T, want the JSON array as text", column, stored)
	}
	var roles []string
	if err := json.Unmarshal([]byte(text), &roles); err != nil {
		t.Fatalf("users.%s holds %q, which is not a JSON array of roles", column, text)
	}
	return roles
}

func (modelKit) selfGrant(t *testing.T, f ModelFixture) {
	if f.Role == "" || f.GrantRole == nil {
		t.Log("the fixture declares no role: there is nothing a registration could grant itself")
		return
	}
	u := users(t, f, account.Hooks{Prepare: func(_ context.Context, _ *account.Registration, a account.Account) error {
		return f.GrantRole(a, f.Role)
	}})
	if _, err := u.Register(context.Background(), "tenant-a", account.Registration{
		Name: "Mallory", Email: "mallory@example.test", Password: kitPassword,
	}); err == nil {
		t.Fatalf("a registration whose Prepare hook granted the role %s was stored: the user policy must refuse a guest an account with privileges", f.Role)
	}
	var count int64
	if err := f.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("users stored after the refusal = %d (%v), want 0", count, err)
	}
}

func (modelKit) publicNames(t *testing.T, f ModelFixture) {
	u := users(t, f, account.Hooks{})
	if _, err := u.PublicNames(context.Background(), auth.Subject{}, []string{"user-a"}); err == nil {
		t.Fatal("a subject with no tenant was answered the public names")
	}
}

func (modelKit) verification(t *testing.T, f ModelFixture) {
	ctx := context.Background()
	u := users(t, f, account.Hooks{})
	registered, err := u.Register(ctx, "tenant-a", account.Registration{Name: "Ana", Email: "ana@example.test", Password: kitPassword})
	if err != nil {
		t.Fatalf("registering: %v", err)
	}
	verificationAgrees(t, f, registered, false)

	verified, changed, err := u.MarkVerified(ctx, "tenant-a", registered.AccountID(), registered.AccountEmail())
	if err != nil || !changed {
		t.Fatalf("verifying the address = %v, %v; want a change", changed, err)
	}
	verificationAgrees(t, f, verified, true)

	seeded, err := u.Provision(ctx, "tenant-a", account.Draft{Name: "Admin", Email: "admin@example.test", Password: kitPassword, Verified: true})
	if err != nil {
		t.Fatalf("provisioning a verified account: %v", err)
	}
	verificationAgrees(t, f, seeded, true)
	found, err := u.Lookup(ctx, "tenant-a", seeded.AccountEmail())
	if err != nil {
		t.Fatalf("reading the provisioned account back: %v", err)
	}
	verificationAgrees(t, f, found, true)
}

// verificationAgrees holds the model, its session subject and its JSON to one
// answer about the verification, and the JSON to carrying no password.
func verificationAgrees(t *testing.T, f ModelFixture, a account.Account, want bool) {
	t.Helper()
	for _, problem := range verificationProblems(f, a, want) {
		t.Error(problem)
	}
}

// verificationProblems is what verificationAgrees reports.
func verificationProblems(f ModelFixture, a account.Account, want bool) []string {
	var problems []string
	if a.IsVerified() != want {
		problems = append(problems, fmt.Sprintf("IsVerified() = %v, want %v", a.IsVerified(), want))
	}
	s, err := subjectFrom(f.Subject, a)
	if err != nil {
		return append(problems, err.Error())
	}
	if s.Verified != want {
		problems = append(problems, fmt.Sprintf("the session subject's Verified = %v, want %v", s.Verified, want))
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return append(problems, "marshalling the account: "+err.Error())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return append(problems, fmt.Sprintf("the account's JSON is not an object: %v: %s", err, encoded))
	}
	raw, has := fields["verified_at"]
	if has && string(raw) == "null" {
		has = false
	}
	if has != want {
		problems = append(problems, fmt.Sprintf("the account's JSON carries verified_at = %v, want %v: %s", has, want, encoded))
	}
	if want && has {
		var at time.Time
		if err := json.Unmarshal(raw, &at); err != nil || at.IsZero() {
			problems = append(problems, fmt.Sprintf("the account's JSON carries verified_at %s, want an instant: %s", raw, encoded))
		}
	}
	if _, exposed := fields["password"]; exposed {
		problems = append(problems, fmt.Sprintf("the account's JSON carries the password field: %s", encoded))
	}
	if hash := a.PasswordHash(); hash != "" && strings.Contains(string(encoded), hash) {
		problems = append(problems, fmt.Sprintf("the account's JSON carries the password hash: %s", encoded))
	}
	return problems
}
