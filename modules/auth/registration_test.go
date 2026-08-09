package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// TestAGuestMayRegisterAndMayNotMintAnAdministrator.
//
// This is the one that matters about self-registration. A registration form is
// the only door into the application that nobody has authenticated through, and
// the question it asks the policy is not "may a guest create a user" but "may a
// guest create THIS user".
//
// The check is on the candidate, so a Roles field added to RegisterRequest by a
// later hand still arrives here and is still refused. That is the difference
// between a rule and a habit.
func TestAGuestMayRegisterAndMayNotMintAnAdministrator(t *testing.T) {
	policy := auth.UserPolicy{}
	guest := security.Guest("acme")

	plain := auth.User{TenantID: "acme", Email: "reader@example.com"}
	if _, err := security.Authorize(context.Background(), policy, guest, auth.ActionUserCreate, plain); err != nil {
		t.Fatalf("a guest could not register: %v", err)
	}

	escalated := auth.User{TenantID: "acme", Email: "reader@example.com", Roles: []string{"admin"}}
	_, err := security.Authorize(context.Background(), policy, guest, auth.ActionUserCreate, escalated)
	if err == nil {
		t.Fatal("a guest registered as an administrator")
	}
}

// TestAnAnonymousSubjectIsStillRefused.
//
// The guest above is declared -- security.Guest sets a marker nothing else can.
// An empty Subject is what a failed session load produces, and it must not reach
// the policy, or every handler that forgot to check its error registers users as
// nobody.
func TestAnAnonymousSubjectIsStillRefused(t *testing.T) {
	_, err := security.Authorize(context.Background(), auth.UserPolicy{}, security.Subject{},
		auth.ActionUserCreate, auth.User{TenantID: "acme"})
	if err == nil {
		t.Fatal("an empty subject was authorized to create a user")
	}
}

// TestAGuestMayDoNothingElse: registration is the one exception, and an
// exception that widens by itself is not one.
func TestAGuestMayDoNothingElse(t *testing.T) {
	guest := security.Guest("acme")
	target := auth.User{ID: "u1", TenantID: "acme", Email: "someone@example.com"}

	for _, a := range []security.Action{
		auth.ActionUserView, auth.ActionUserUpdate, auth.ActionUserDelete,
	} {
		if _, err := security.Authorize(context.Background(), auth.UserPolicy{}, guest, a, target); err == nil {
			t.Errorf("a guest was granted %s", a)
		}
	}
}

// TestRegistrationValidatesBeforeItHashes.
//
// Argon2 is expensive on purpose. An unvalidated password reaching the hash is a
// form that costs the server real work per submission, which is what turns a
// registration endpoint into a denial of service.
func TestRegistrationValidatesBeforeItHashes(t *testing.T) {
	for _, c := range []struct {
		name  string
		in    auth.RegisterRequest
		field string
	}{
		{"no name", auth.RegisterRequest{Email: "a@b.com", Password: strings.Repeat("x", 12), PasswordConfirmation: strings.Repeat("x", 12)}, "name"},
		{"not an address", auth.RegisterRequest{Name: "Ada", Email: "ada", Password: strings.Repeat("x", 12), PasswordConfirmation: strings.Repeat("x", 12)}, "email"},
		{"short password", auth.RegisterRequest{Name: "Ada", Email: "a@b.com", Password: "short", PasswordConfirmation: "short"}, "password"},
		{"confirmation differs", auth.RegisterRequest{Name: "Ada", Email: "a@b.com", Password: strings.Repeat("x", 12), PasswordConfirmation: strings.Repeat("y", 12)}, "password_confirmation"},
		{"confirmation missing", auth.RegisterRequest{Name: "Ada", Email: "a@b.com", Password: strings.Repeat("x", 12)}, "password_confirmation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			errs := c.in.Validate()
			if !errs.Any() {
				t.Fatal("accepted")
			}
			if _, ok := errs[c.field]; !ok {
				t.Errorf("the error is not on %s: %v", c.field, errs)
			}
		})
	}
}

// TestAValidRegistrationHasNothingToSayAboutIt.
func TestAValidRegistrationHasNothingToSayAboutIt(t *testing.T) {
	in := auth.RegisterRequest{
		Name:                 "Ada Lovelace",
		Email:                "ada@example.com",
		Password:             "a-long-enough-password",
		PasswordConfirmation: "a-long-enough-password",
	}
	if errs := in.Validate(); errs.Any() {
		t.Fatalf("a correct form was refused: %v", errs)
	}
	var _ validation.Validatable = in
}

// TestAnUnverifiedUserIsUnverifiedAndNotJustZero.
//
// The zero time.Time writes to SQL as 0001-01-01, which is a date -- so a query
// asking `WHERE verified_at IS NOT NULL` answers true for every user who never
// verified anything. That exact mistake shipped once in this repository, on the
// posts table, and it is why the column is written through nullTime.
func TestAnUnverifiedUserIsUnverifiedAndNotJustZero(t *testing.T) {
	if (auth.User{}).Verified() {
		t.Fatal("a user with no verification timestamp reports as verified")
	}
}
