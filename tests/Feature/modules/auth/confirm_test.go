package feature

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

// Re-typing the password on a session that is already open.
//
// The screen behind it is reached by somebody who is signed in, which is why it
// needs a throttle of its own: whoever is holding a stolen session cookie is
// already past every other check, and an unlimited "is this the password?"
// endpoint hands them the one thing the cookie does not give -- the password
// itself, which is the one they will try on the next site.

const confirmTenant = "t1"

func TestTheRightPasswordConfirmsAndTheWrongOneDoesNot(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u := accountWithPassword(t, db, "u1", confirmTenant, "ana@example.com", rightWord)
	sub := auth.SubjectOf(u)

	if err := svc.ConfirmPassword(ctx, sub, rightWord, signInHome); err != nil {
		t.Fatalf("the account's own password was refused: %v", err)
	}
	if err := svc.ConfirmPassword(ctx, sub, wrongWord, signInHome); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("a wrong password answered %v, and a confirmation that accepts one confirms nothing", err)
	}
}

func TestGuessingAtTheConfirmationScreenIsRefusedAfterFiveTries(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u := accountWithPassword(t, db, "u1", confirmTenant, "ana@example.com", rightWord)
	sub := auth.SubjectOf(u)

	for i := 0; i < security.MaxSignInFailures; i++ {
		if err := svc.ConfirmPassword(ctx, sub, wrongWord, signInAway); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("guess %d answered %v, want the invalid credentials the budget is counted from", i+1, err)
		}
	}

	err := svc.ConfirmPassword(ctx, sub, wrongWord, signInAway)
	var locked auth.TooManyAttemptsError
	if !errors.As(err, &locked) {
		t.Fatalf("the sixth guess in a minute was answered with %v: a screen behind RequireAuth is then a password "+
			"oracle for anybody holding a stolen session cookie", err)
	}
}

// The confirmation budget is kept apart from the sign-in one, so that somebody
// guessing at this screen cannot also take the sign-in form away from the
// account's owner. What still bounds them is the throttle's per-address budget,
// which both spend from.
func TestGuessingAtTheConfirmationScreenDoesNotLockTheOwnerOutOfSigningIn(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u := accountWithPassword(t, db, "u1", signInTenant, "ana@example.com", rightWord)
	sub := auth.SubjectOf(u)

	for i := 0; i <= security.MaxSignInFailures; i++ {
		_ = svc.ConfirmPassword(ctx, sub, wrongWord, signInAway)
	}

	if _, err := svc.Authenticate(ctx, signInTenant, "ana@example.com", rightWord, signInHome); err != nil {
		t.Fatalf("signing in from her own address: %v -- somebody with a stolen cookie could then lock the owner "+
			"out of the account they are stealing", err)
	}
}

// Confirming successfully forgets the tries before it, the same way signing in
// does: somebody who remembers their password on the fifth attempt must not be
// refused by the four that came before.
func TestConfirmingSuccessfullyForgetsTheTriesBeforeIt(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u := accountWithPassword(t, db, "u1", confirmTenant, "ana@example.com", rightWord)
	sub := auth.SubjectOf(u)

	for i := 0; i < security.MaxSignInFailures-1; i++ {
		_ = svc.ConfirmPassword(ctx, sub, wrongWord, signInHome)
	}
	if err := svc.ConfirmPassword(ctx, sub, rightWord, signInHome); err != nil {
		t.Fatalf("the right password on the fifth try was refused: %v", err)
	}

	for i := 0; i < security.MaxSignInFailures-1; i++ {
		if err := svc.ConfirmPassword(ctx, sub, wrongWord, signInHome); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("after a successful confirmation, try %d answered %v: the failures before it were not forgotten", i+1, err)
		}
	}
}

// An empty subject is a session that failed to load, and it is answered with
// neither a yes nor "your password is wrong": the handler's answer to that is
// the sign-in screen, and telling somebody their own password was wrong when
// their session had expired is how a support ticket gets opened.
func TestConfirmingForNobodyIsNeitherAcceptedNorCalledAWrongPassword(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	accountWithPassword(t, db, "u1", confirmTenant, "ana@example.com", rightWord)

	err := svc.ConfirmPassword(context.Background(), security.Subject{}, rightWord, signInHome)
	if err == nil {
		t.Fatal("confirming a password for nobody succeeded")
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("a session that failed to load was reported as a wrong password")
	}
}

// A subject naming an account that is not there any more -- deleted while the
// session was open -- is refused rather than let through.
func TestASessionNamingAnAccountThatIsGoneCannotConfirm(t *testing.T) {
	svc, _ := serviceOverFakeDB(t)

	err := svc.ConfirmPassword(context.Background(),
		security.Subject{ID: "u-deleted", Tenant: confirmTenant}, rightWord, signInHome)
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("err = %v, and a session whose account is gone must not confirm anything", err)
	}
}
