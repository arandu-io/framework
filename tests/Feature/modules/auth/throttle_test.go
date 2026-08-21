package feature

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

// What these are about: the sign-in throttle lives under Authenticate, not in a
// handler, and these tests reach it the only way an application can -- through
// Authenticate. A control that only the framework's own screen enforces is a
// control that the starter kit's published screen, which the project owns and
// may delete, would remove without noticing.

const (
	signInTenant = "t1"
	signInHome   = "ip:198.51.100.7"
	signInAway   = "ip:203.0.113.9"
	rightWord    = "correct-horse-battery"
	wrongWord    = "incorrect-horse-battery"
)

// wrongPasswordUntilLockedOut spends the whole budget and returns the answer to
// the attempt after it.
func wrongPasswordUntilLockedOut(t *testing.T, svc *auth.Service, email, client string) error {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < security.MaxSignInFailures; i++ {
		_, err := svc.Authenticate(ctx, signInTenant, email, wrongWord, client)
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d answered %v, want the invalid credentials the budget is counted from", i+1, err)
		}
	}
	_, err := svc.Authenticate(ctx, signInTenant, email, wrongWord, client)
	return err
}

// withoutTheCountdown is the message with every digit blanked, so two lockouts
// measured a fraction of a second apart compare equal.
func withoutTheCountdown(err error) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'N'
		}
		return r
	}, err.Error())
}

func TestGuessingStopsBeingAnsweredAfterFiveWrongPasswords(t *testing.T) {
	svc, _ := serviceOverFakeDB(t)

	err := wrongPasswordUntilLockedOut(t, svc, "ana@example.com", signInAway)

	var locked auth.TooManyAttemptsError
	if !errors.As(err, &locked) {
		t.Fatalf("the sixth wrong password in a minute was answered with %v: a stolen password list is being tried "+
			"as fast as the process can hash", err)
	}
	if locked.Seconds() < 1 || locked.Seconds() > int(security.SignInWindow.Seconds()) {
		t.Errorf("the screen would say to come back in %d seconds, out of a lockout that lasts %v",
			locked.Seconds(), security.SignInWindow)
	}
}

// TestAllTheGuessesArrivingAtOnceStillOnlyGetFive is the same property as the
// unit test in package security, asserted where an attacker actually reaches it:
// nothing about a sign-in form makes anybody send the sixth guess after the
// fifth was answered.
//
// It failed before the throttle took its unit up front. Eight requests fired
// together against a budget of five had all eight passwords checked, because
// each of them asked whether the account was locked out while the other seven
// were still inside an argon2 hash and had recorded nothing.
func TestAllTheGuessesArrivingAtOnceStillOnlyGetFive(t *testing.T) {
	svc, _ := serviceOverFakeDB(t)
	const atOnce = 16

	var checked int
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < atOnce; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Authenticate(context.Background(), signInTenant, "ana@example.com", wrongWord, signInAway)
			var locked auth.TooManyAttemptsError
			if errors.As(err, &locked) {
				return
			}
			mu.Lock()
			checked++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if checked > security.MaxSignInFailures {
		t.Fatalf("%d of %d passwords sent at the same instant were checked against a budget of %d: an attacker "+
			"who opens sockets instead of waiting for answers is not throttled at all",
			checked, atOnce, security.MaxSignInFailures)
	}
}

func TestAnAccountIsNotLockedOutOfItsOwnAddressBySomebodyGuessingFromElsewhere(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	hash, err := security.HashPassword(rightWord)
	if err != nil {
		t.Fatalf("hashing the password: %v", err)
	}
	db.seedUser(auth.User{ID: "u1", TenantID: signInTenant, Email: "ana@example.com", Password: hash})

	// Two strangers, from two addresses, guessing at one account.
	wrongPasswordUntilLockedOut(t, svc, "ana@example.com", signInAway)
	wrongPasswordUntilLockedOut(t, svc, "ana@example.com", "ip:192.0.2.44")

	if _, err := svc.Authenticate(context.Background(), signInTenant, "ana@example.com", rightWord, signInHome); err != nil {
		t.Fatalf("signing in from her own address: %v -- anyone who knows an address can now lock its owner out "+
			"by guessing at it, which is the denial of service the lockout was meant to prevent", err)
	}
}

func TestSigningInSuccessfullyForgetsTheFailuresBeforeIt(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	hash, err := security.HashPassword(rightWord)
	if err != nil {
		t.Fatalf("hashing the password: %v", err)
	}
	db.seedUser(auth.User{ID: "u1", TenantID: signInTenant, Email: "ana@example.com", Password: hash})

	// Four tries to remember which password this application knows, and then it.
	for i := 0; i < security.MaxSignInFailures-1; i++ {
		if _, err := svc.Authenticate(ctx, signInTenant, "ana@example.com", wrongWord, signInHome); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d answered %v, want invalid credentials", i+1, err)
		}
	}
	if _, err := svc.Authenticate(ctx, signInTenant, "ana@example.com", rightWord, signInHome); err != nil {
		t.Fatalf("the right password on the fifth try was refused: %v", err)
	}

	// The same again, on the other side of it. Without the clear, the count from
	// before the successful sign-in would still be there and the second wrong
	// password of the day would lock her out.
	for i := 0; i < security.MaxSignInFailures-1; i++ {
		if _, err := svc.Authenticate(ctx, signInTenant, "ana@example.com", wrongWord, signInHome); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("after a successful sign-in, attempt %d answered %v: the failures before it were not forgotten", i+1, err)
		}
	}
}

func TestADatabaseThatIsDownDoesNotLockEveryoneOut(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	hash, err := security.HashPassword(rightWord)
	if err != nil {
		t.Fatalf("hashing the password: %v", err)
	}
	db.seedUser(auth.User{ID: "u1", TenantID: signInTenant, Email: "ana@example.com", Password: hash})

	db.usersTableFails(true)
	for i := 0; i < 3*security.MaxSignInFailures; i++ {
		_, err := svc.Authenticate(ctx, signInTenant, "ana@example.com", rightWord, signInHome)
		if errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d called an outage a wrong password, and the person who reads that message "+
				"will spend the incident resetting a password that was never wrong", i+1)
		}
		var locked auth.TooManyAttemptsError
		if errors.As(err, &locked) {
			t.Fatalf("attempt %d was counted against the budget: an outage would lock every account in the "+
				"database out for a minute after it came back", i+1)
		}
		if err == nil {
			t.Fatalf("attempt %d signed in through a database that was refusing to answer", i+1)
		}
	}

	db.usersTableFails(false)
	if _, err := svc.Authenticate(ctx, signInTenant, "ana@example.com", rightWord, signInHome); err != nil {
		t.Fatalf("the first sign-in after the database came back: %v", err)
	}
}

func TestTheLockoutDoesNotSayWhetherTheAccountExists(t *testing.T) {
	// Two applications, identical but for one row: in one the address is
	// registered, in the other it never was. The lockout has to read the same
	// from the outside, or it is a better enumeration oracle than the one
	// ErrInvalidCredentials exists to close -- one question, no guessing.
	registered, db := serviceOverFakeDB(t)
	hash, err := security.HashPassword(rightWord)
	if err != nil {
		t.Fatalf("hashing the password: %v", err)
	}
	db.seedUser(auth.User{ID: "u1", TenantID: signInTenant, Email: "ana@example.com", Password: hash})
	stranger, _ := serviceOverFakeDB(t)

	real := wrongPasswordUntilLockedOut(t, registered, "ana@example.com", signInAway)
	fake := wrongPasswordUntilLockedOut(t, stranger, "ana@example.com", signInAway)

	var lockedReal, lockedFake auth.TooManyAttemptsError
	if !errors.As(real, &lockedReal) || !errors.As(fake, &lockedFake) {
		t.Fatalf("one of the two was not locked out at all: registered %v, unknown %v", real, fake)
	}
	// The countdown is a clock and not information about the account, so it is
	// the only part allowed to differ.
	if withoutTheCountdown(real) != withoutTheCountdown(fake) {
		t.Fatalf("the lockout reads %q for an account that exists and %q for one that does not: that is the answer "+
			"an attacker wanted", real.Error(), fake.Error())
	}
	if strings.Contains(real.Error(), "ana@example.com") {
		t.Errorf("the message repeats the address back: %q", real.Error())
	}

	// Nothing was asked of the users table to produce it, which is why it cannot
	// differ: the count before the lockout is the count after it.
	before := len(db.statements())
	if _, err := registered.Authenticate(context.Background(), signInTenant, "ana@example.com", rightWord, signInAway); err == nil {
		t.Fatal("the right password got in during the lockout")
	}
	if after := len(db.statements()); after != before {
		t.Errorf("a locked-out sign-in ran %d statements against the users table: the answer can only be identical "+
			"for a registered address and an unknown one if neither is looked up", after-before)
	}
}
