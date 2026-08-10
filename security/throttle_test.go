package security

// The only test in this package written from the inside, and the clock is why:
// the window is a constant, so proving that it closes from the outside means
// sleeping for a minute in the suite. Everything asserted below is still
// behaviour of the exported API.

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type testClock struct{ t time.Time }

func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// throttleAt returns a throttle reading the given clock.
func throttleAt(c *testClock) *MemoryThrottle {
	th := NewMemoryThrottle()
	th.now = func() time.Time { return c.t }
	th.lastSweep = c.t
	return th
}

const (
	tenant = "acme"
	owner  = "ana@example.com"
	home   = "ip:198.51.100.7"
	away   = "ip:203.0.113.9"
)

func spendTheBudget(th *MemoryThrottle, tenant, identity, client string) {
	for i := 0; i < MaxSignInFailures; i++ {
		th.Attempt(tenant, identity, client)
	}
}

func refused(th *MemoryThrottle, tenant, identity, client string) bool {
	_, ok := th.Attempt(tenant, identity, client)
	return !ok
}

func TestTheCounterOpensAgainWhenTheWindowHasPassed(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	spendTheBudget(th, tenant, owner, home)
	if !refused(th, tenant, owner, home) {
		t.Fatal("five wrong passwords in a row were not enough to lock the account out")
	}

	clock.advance(SignInWindow - time.Second)
	if !refused(th, tenant, owner, home) {
		t.Error("the lockout ended before the window it promised did")
	}

	clock.advance(2 * time.Second)
	if refused(th, tenant, owner, home) {
		t.Error("somebody who waited out the lockout still cannot sign in, and nothing tells them when they can")
	}
}

// TestABurstOfSimultaneousGuessesGetsNoMoreThanAPatientOne is the property the
// first version of this file did not have, and the one an attacker reaches for
// first: there is no reason to send the sixth guess after the fifth is answered
// when all six can be sent at once.
//
// It was a real hole and not a theoretical one. The throttle used to answer a
// question and be told the outcome afterwards, with an argon2 hash in between,
// and a budget of five let eight simultaneous requests through -- as many as the
// attacker opened sockets for.
func TestABurstOfSimultaneousGuessesGetsNoMoreThanAPatientOne(t *testing.T) {
	th := NewMemoryThrottle()
	const atOnce = 64

	var allowed int
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < atOnce; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, ok := th.Attempt(tenant, owner, away); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed > MaxSignInFailures {
		t.Fatalf("%d of %d guesses sent at the same instant were all let through against a budget of %d: "+
			"the lockout only slows down an attacker who waits politely for each answer",
			allowed, atOnce, MaxSignInFailures)
	}
}

func TestRememberingThePasswordOnTheLastTryDoesNotLockThePersonOut(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	for i := 0; i < MaxSignInFailures-1; i++ {
		th.Attempt(tenant, owner, home)
	}
	// The attempt that turned out to be the right password, and the forgetting
	// that follows it.
	th.Attempt(tenant, owner, home)
	th.Clear(tenant, owner, home)

	// The whole budget again, on the other side of the successful sign-in.
	for i := 0; i < MaxSignInFailures; i++ {
		if refused(th, tenant, owner, home) {
			t.Fatalf("signing in successfully did not forget the %d failures before it: refused on try %d of a "+
				"fresh budget of %d", MaxSignInFailures-1, i+1, MaxSignInFailures)
		}
	}
}

// TestSigningInDayInDayOutNeverCostsTheAddressAnything is the office behind one
// address. The per-client budget is spent by wrong passwords, and a successful
// sign-in has to give back the unit it took to be checked -- otherwise
// twenty-five people arriving at nine in the morning lock the twenty-sixth out
// of a system nobody was attacking.
func TestSigningInDayInDayOutNeverCostsTheAddressAnything(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	for i := 0; i < 10*MaxSignInFailuresPerClient; i++ {
		who := fmt.Sprintf("colleague-%d@example.com", i)
		if _, ok := th.Attempt(tenant, who, home); !ok {
			t.Fatalf("sign-in %d from the office address was refused, and every one before it had the right password",
				i+1)
		}
		th.Clear(tenant, who, home)
	}
	if th.Len() != 0 {
		t.Errorf("an application whose sign-ins all succeeded is holding %d counters", th.Len())
	}
}

func TestSprayingOneAccountFromElsewhereDoesNotLockItsOwnerOut(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	// Two attackers, from two addresses, guessing at one account.
	spendTheBudget(th, tenant, owner, away)
	spendTheBudget(th, tenant, owner, "ip:192.0.2.44")

	if refused(th, tenant, owner, home) {
		t.Fatal("anyone who knows an address can now lock its owner out of their own account by guessing at it")
	}
}

func TestOneAddressCannotWalkAListOfAccounts(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	// One guess each against a stolen list, which is what the per-account
	// budget alone would let through forever.
	for i := 0; i < MaxSignInFailuresPerClient; i++ {
		th.Attempt(tenant, fmt.Sprintf("victim-%d@example.com", i), away)
	}

	if !refused(th, tenant, "victim-never-touched@example.com", away) {
		t.Fatalf("one address spent %d guesses across %d accounts and is still welcome to try the next one",
			MaxSignInFailuresPerClient, MaxSignInFailuresPerClient)
	}
	if refused(th, tenant, owner, home) {
		t.Error("the address that was walking the list took an unrelated address down with it")
	}
}

func TestAMillionAddressesTypedIntoTheFormAreNotAMillionCounters(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	for i := 0; i < 100_000; i++ {
		th.Attempt(tenant, fmt.Sprintf("guess-%d@example.com", i), away)
	}

	// One counter per address it was allowed to spend, plus the address's own.
	if want := MaxSignInFailuresPerClient + 1; th.Len() > want {
		t.Fatalf("a hundred thousand addresses typed into one form left %d counters in memory, want at most %d: "+
			"the thing that counts the attack is the thing the attack exhausts", th.Len(), want)
	}
}

func TestABotnetCannotGrowTheTablePastItsCap(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	// Two counters per address -- the pair and the address itself -- so this is
	// more than the cap even though every one of them is a first failure.
	for i := 0; i < maxTrackedSignIns; i++ {
		th.Attempt(tenant, fmt.Sprintf("victim-%d@example.com", i), fmt.Sprintf("ip:10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff))
	}

	if th.Len() > maxTrackedSignIns {
		t.Fatalf("the table holds %d counters, want at most %d: the process grows with the attack", th.Len(), maxTrackedSignIns)
	}
}

func TestTheLockoutSaysHowMuchOfItIsLeft(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	spendTheBudget(th, tenant, owner, home)
	clock.advance(SignInWindow / 2)

	retry, ok := th.Attempt(tenant, owner, home)
	if ok {
		t.Fatal("the lockout ended halfway through the window it promised")
	}
	if retry <= 0 || retry > SignInWindow/2 {
		t.Fatalf("the screen would tell somebody to come back in %v, halfway through a %v lockout", retry, SignInWindow)
	}
}

func TestOneTenantCannotLockAnotherTenantsAccountOut(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	spendTheBudget(th, tenant, owner, home)

	if refused(th, "globex", owner, home) {
		t.Fatal("failing to sign in to one customer's application locked the same person out of another's")
	}
}

func TestOneAccountSpelledInTwoCasesIsOneBudget(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	for i := 0; i < MaxSignInFailures-2; i++ {
		th.Attempt(tenant, "Ana@Example.COM", home)
	}
	for i := 0; i < 2; i++ {
		th.Attempt(tenant, "  ana@example.com  ", home)
	}

	if !refused(th, tenant, owner, home) {
		t.Fatal("changing the case of the address bought another five guesses, and the users table treats it as one account")
	}
}

// TestHangingUpMidAttemptIsWorthNothingAndNoMore covers both halves of Refund at
// once, and the second half is the dangerous one.
//
// An attempt that never reached a credential has to be given back, or a database
// outage spends every account's budget. But it must be given back and not
// forgiven: cancelling a request is something the attacker chooses, so if an
// abandoned attempt wiped the count then four guesses and one hang-up, over and
// over, would be an unlimited number of guesses.
func TestHangingUpMidAttemptIsWorthNothingAndNoMore(t *testing.T) {
	clock := &testClock{t: time.Now()}
	th := throttleAt(clock)

	for i := 0; i < MaxSignInFailures-1; i++ {
		th.Attempt(tenant, owner, home)
	}

	// A hundred attempts that hung up before the users table answered.
	for i := 0; i < 100; i++ {
		if _, ok := th.Attempt(tenant, owner, home); !ok {
			t.Fatalf("abandoned attempt %d was refused: giving the unit back did not give it back", i+1)
		}
		th.Refund(tenant, owner, home)
	}

	if refused(th, tenant, owner, home) {
		t.Fatal("the attempts that hung up were counted anyway: a database outage would lock every account out")
	}
	if !refused(th, tenant, owner, home) {
		t.Fatal("a hundred abandoned attempts bought a hundred more guesses -- hanging up is the way out of the lockout")
	}
}
