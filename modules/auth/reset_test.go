package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

// The password reset, from the side an attacker holds it: a link that is signed
// and stored nowhere, and that has to stop working once it has been used.
//
// The thing it replaced was a package-level map. Two replicas each had their own,
// so the link worked on whichever one issued it; a restart threw every link in
// flight away; nothing swept what nobody clicked; and the address was mailed
// without ever being looked up, which made the endpoint a way to send mail from
// this application's domain to anybody, on request.

const resetTenant = "t1"

// accountWithPassword seeds one account whose password is known, and returns it
// as the row a lookup will find.
func accountWithPassword(t *testing.T, db *fakeDB, id, tenant, email, plain string) auth.User {
	t.Helper()
	hash, err := security.HashPassword(plain)
	if err != nil {
		t.Fatalf("hashing the password: %v", err)
	}
	u := auth.User{ID: id, TenantID: tenant, Email: email, Password: hash}
	db.seedUser(u)
	return u
}

func TestUsingAResetLinkOnceStopsItWorkingASecondTime(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u := accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	link := auth.ResetPayload(u)
	if _, err := svc.ResetPassword(ctx, link, u.Email, "a-brand-new-password"); err != nil {
		t.Fatalf("the first use of the link: %v", err)
	}

	_, err := svc.ResetPassword(ctx, link, u.Email, "a-third-password")
	if !errors.Is(err, auth.ErrResetLinkSpent) {
		t.Fatalf("the same link changed the password a second time (%v): a reset link somebody forwarded, or one "+
			"read out of a mailbox weeks later, is a live way into the account", err)
	}
}

// Every link minted before the change dies with it, not only the one that was
// used. The old design deleted the row it was handed and left the siblings live,
// so asking three times for a link left two of them working after the reset.
func TestTheOlderLinksDieWithTheOneThatWasUsed(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u := accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	first := auth.ResetPayload(u)
	second := auth.ResetPayload(u)

	if _, err := svc.ResetPassword(ctx, second, u.Email, "a-brand-new-password"); err != nil {
		t.Fatalf("using the newer link: %v", err)
	}

	if _, err := svc.ResetPassword(ctx, first, u.Email, "a-fourth-password"); !errors.Is(err, auth.ErrResetLinkSpent) {
		t.Fatalf("the earlier link still worked after the password changed (%v): asking for a link twice would "+
			"leave two ways in", err)
	}
}

// RULE 14 on the one request in the application that arrives with no session to
// take a tenant from. The link names its own, signed, so the host it is opened
// at decides nothing.
func TestALinkFromOneTenantDoesNotResetTheAccountWithThatAddressInAnother(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()

	const shared = "ana@example.com"
	acme := accountWithPassword(t, db, "u-acme", "acme", shared, rightWord)
	accountWithPassword(t, db, "u-globex", "globex", shared, "the-other-companys-password")

	if _, err := svc.ResetPassword(ctx, auth.ResetPayload(acme), shared, "a-brand-new-password"); err != nil {
		t.Fatalf("resetting the account the link was minted for: %v", err)
	}

	// The other tenant's account still answers to its own password, which it
	// would not if the reset had been resolved by address alone.
	other, err := svc.Lookup(ctx, "globex", shared)
	if err != nil {
		t.Fatalf("reading the other tenant's account: %v", err)
	}
	if err := security.VerifyPassword("the-other-companys-password", other.Password); err != nil {
		t.Fatal("a link minted in one tenant changed the password of the account with the same address in another: " +
			"one customer's forgotten-password form is a way into another customer's account")
	}
}

// The separator property security.Signer's own header comment is about, held
// against this payload: with a plain separator, a tenant that ends where the id
// begins is a link minted for one account that reads back as another.
func TestNothingInsideAFieldCanMoveTheBoundaryBetweenFields(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()

	// Two accounts whose fields, run together without lengths, spell the same
	// bytes: tenant "a" + id "b:c" against tenant "a:b" + id "c".
	victim := accountWithPassword(t, db, "b:c", "a", "victim@example.com", rightWord)
	accountWithPassword(t, db, "c", "a:b", "attacker@example.com", "the-attackers-own-password")

	payload := auth.ResetPayload(victim)
	if !strings.Contains(payload, "3:b:c") {
		t.Fatalf("the id is not written with its length in front of it: %q", payload)
	}

	// And the honest half: the payload still resolves to the account it names.
	if _, err := svc.ResetPassword(ctx, payload, victim.Email, "a-brand-new-password"); err != nil {
		t.Fatalf("a payload whose fields contain the separator did not resolve: %v", err)
	}
}

// The address is compared with the one the link was minted for. Without it the
// Required e-mail field on the reset form is decoration: somebody could type any
// address and the token would still reset whichever account it named.
func TestTheAddressTypedOnTheFormHasToBeTheOneTheLinkWasSentTo(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	u := accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	_, err := svc.ResetPassword(context.Background(), auth.ResetPayload(u), "somebody.else@example.com", "a-brand-new-password")
	if !errors.Is(err, auth.ErrResetLinkSpent) {
		t.Fatalf("the form's address was ignored: %v", err)
	}
}

// Address changed after the mail went out: the link proved control of the old
// address and nothing about the new one.
func TestALinkStopsWorkingWhenTheAccountsAddressChanges(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	u := accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	payload := auth.ResetPayload(u)
	db.changeAddress(u.ID, "ana@newplace.example.com")

	_, err := svc.ResetPassword(context.Background(), payload, "ana@example.com", "a-brand-new-password")
	if !errors.Is(err, auth.ErrResetLinkSpent) {
		t.Fatalf("a link mailed to the old address reset the account at its new one: %v", err)
	}
}

// The lookup that decides whether to send anything is throttled, and it is
// throttled in the service rather than in the published screen -- the screen is
// the project's file and can be edited away.
func TestAskingForALinkSixTimesInAMinuteIsRefused(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	for i := 0; i < security.MaxSignInFailures; i++ {
		if _, err := svc.FindForReset(ctx, resetTenant, "ana@example.com", signInAway); err != nil {
			t.Fatalf("request %d for a reset link: %v", i+1, err)
		}
	}

	_, err := svc.FindForReset(ctx, resetTenant, "ana@example.com", signInAway)
	var locked auth.TooManyAttemptsError
	if !errors.As(err, &locked) {
		t.Fatalf("the sixth request in a minute was answered with %v: an unthrottled form sends mail from this "+
			"application's domain to an address of the caller's choosing, as often as they ask", err)
	}
}

// The reset budget is kept apart from the sign-in one, and this is the reason:
// somebody who has just got their password wrong five times is exactly the
// person who clicks "forgot my password", and a shared counter would refuse them
// the recovery at the one moment they want it.
func TestFailingToSignInDoesNotAlsoRefuseTheRecovery(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	accountWithPassword(t, db, "u1", signInTenant, "ana@example.com", rightWord)

	if err := wrongPasswordUntilLockedOut(t, svc, "ana@example.com", signInHome); err == nil {
		t.Fatal("the sign-in budget was not spent, so this test is not measuring what it says")
	}

	if _, err := svc.FindForReset(context.Background(), signInTenant, "ana@example.com", signInHome); err != nil {
		t.Fatalf("asking for a reset link after five wrong passwords: %v -- the recovery is refused at the one "+
			"moment somebody needs it", err)
	}
}

// The counter is keyed by the client as well as the address, so spending a
// stranger's budget from a stranger's address is spending your own.
func TestSprayingOneAccountFromElsewhereDoesNotLockItsOwnerOut(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	for i := 0; i <= security.MaxSignInFailures; i++ {
		_, _ = svc.FindForReset(ctx, resetTenant, "ana@example.com", signInAway)
	}

	if _, err := svc.FindForReset(ctx, resetTenant, "ana@example.com", signInHome); err != nil {
		t.Fatalf("the owner could not ask for a link from her own address: %v -- anyone who knows an address could "+
			"then take the recovery away from whoever owns it", err)
	}
}

// The answer to an unknown address is ErrUserNotFound and nothing else, so the
// screen has one shape to render for both. It used to mail whatever was typed
// without looking anything up at all.
func TestAnUnknownAddressIsNotLookedUpIntoAMessage(t *testing.T) {
	svc, _ := serviceOverFakeDB(t)

	_, err := svc.FindForReset(context.Background(), resetTenant, "nobody@example.com", signInAway)
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("an unregistered address answered %v, and the caller has no way to tell it must send nothing", err)
	}
}

// An outage is not a request. Counting it would spend every account's budget in
// one minute and refuse the whole tenant its recovery once the database came
// back -- the same branch Authenticate has, for the same reason.
func TestADatabaseThatIsDownDoesNotSpendTheRecoveryBudget(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	db.usersTableFails(true)
	for i := 0; i < 3*security.MaxSignInFailures; i++ {
		_, err := svc.FindForReset(ctx, resetTenant, "ana@example.com", signInAway)
		var locked auth.TooManyAttemptsError
		if errors.As(err, &locked) {
			t.Fatalf("request %d was counted against the budget: an outage would take the password reset away from "+
				"everybody for a minute after it ended", i+1)
		}
	}

	db.usersTableFails(false)
	if _, err := svc.FindForReset(ctx, resetTenant, "ana@example.com", signInAway); err != nil {
		t.Fatalf("the first request after the database came back: %v", err)
	}
}

// A payload nobody minted is refused rather than guessed at. It cannot arrive
// past a valid signature in production, and a parser that fills in what it does
// not understand is one that accepts two spellings of one token.
func TestAPayloadThatIsNotOneWeWroteIsRefused(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	accountWithPassword(t, db, "u1", resetTenant, "ana@example.com", rightWord)

	for _, payload := range []string{
		"", "nonsense", "1:t", "1:t2:u1", "999:t", auth.ResetPayload(auth.User{}) + "trailing",
	} {
		_, err := svc.ResetPassword(context.Background(), payload, "ana@example.com", "a-brand-new-password")
		if !errors.Is(err, auth.ErrResetLinkSpent) {
			t.Errorf("the payload %q was answered with %v, want the one refusal every bad link gets", payload, err)
		}
	}
}
