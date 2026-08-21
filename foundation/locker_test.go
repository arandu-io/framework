// Tests of the lock, which is three lines carrying a guarantee.
//
// What a lock does under contention is tested in hesape, against the store; what
// is left to prove here is that the three lines hand the name, the ttl and the
// work to it in that shape -- that one caller runs and the other does not, that
// the lock comes back when the work finishes, and that a caller can tell "somebody
// else has it" from "the work failed".
//
// The last one is not decoration. The scheduler recognizes a held lock by the
// text of the error, so a refusal this package stopped passing through unchanged
// would read as an acquired lock, and every replica would run every task.

package foundation_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/cache"
)

// newTestLocker returns a locker over a store this test owns, so nothing here
// depends on a server being up.
func newTestLocker() foundation.Locker {
	return foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))
}

// TestOnlyOneCallerRunsAtATime is the whole point: with N replicas, a task
// scheduled every minute runs once a minute and not N times.
//
// The first caller is held inside the work, so the second one asks while the
// lock is genuinely taken rather than in whatever order the scheduler happens
// to produce.
func TestOnlyOneCallerRunsAtATime(t *testing.T) {
	locker := newTestLocker()

	holding := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)

	go func() {
		first <- locker.Run(context.Background(), "reports.monthly", time.Minute,
			func(context.Context) error {
				close(holding)
				<-release
				return nil
			})
	}()

	<-holding

	ran := false
	err := locker.Run(context.Background(), "reports.monthly", time.Minute,
		func(context.Context) error { ran = true; return nil })

	if ran {
		t.Error("the second caller ran the work while the first was still holding the lock")
	}
	if err == nil {
		t.Fatal("the second caller was told the lock was taken")
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatalf("the caller holding the lock: %v", err)
	}
}

// TestTheRefusalSaysTheLockIsHeld is what a caller matches on.
//
// The value is hesape's, passed through rather than wrapped or replaced, so a
// caller testing for it is looking at one error. The text matters as much as the
// value: the scheduler reads this message rather than importing the type.
func TestTheRefusalSaysTheLockIsHeld(t *testing.T) {
	locker := newTestLocker()

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = locker.Run(context.Background(), "outbox.relay", time.Minute,
			func(context.Context) error {
				close(holding)
				<-release
				return nil
			})
	}()
	<-holding

	err := locker.Run(context.Background(), "outbox.relay", time.Minute,
		func(context.Context) error { return nil })

	if !errors.Is(err, cache.ErrLocked) {
		t.Errorf("the refusal is %v, and a caller matching on a held lock will not recognize it", err)
	}
	// The scheduler looks for this substring and treats what carries it as
	// "another replica is doing it". Anything else reads as an acquired lock.
	if err == nil || !strings.Contains(err.Error(), "lock is held") {
		t.Errorf("the refusal reads %q, which the scheduler will not recognize as a held lock", err)
	}

	close(release)
	<-done
}

// TestTheLockIsGivenBackWhenTheWorkFinishes: a lock that is taken and never
// returned makes the task run once and never again until the ttl expires, which
// on a one hour ttl is one run an hour.
//
// Three passes rather than two, because a release that only works the first time
// is a release that works in a test with two.
func TestTheLockIsGivenBackWhenTheWorkFinishes(t *testing.T) {
	locker := newTestLocker()

	for pass := 1; pass <= 3; pass++ {
		ran := false
		err := locker.Run(context.Background(), "invoices.sweep", time.Minute,
			func(context.Context) error { ran = true; return nil })
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if !ran {
			t.Fatalf("pass %d did not run: the previous pass kept the lock", pass)
		}
	}
}

// TestTheLockIsGivenBackWhenTheWorkFails is the same guarantee on the path that
// is easy to get wrong. Work that returns an error still has to give the lock
// back, or one failure stops the task until the ttl expires.
func TestTheLockIsGivenBackWhenTheWorkFails(t *testing.T) {
	locker := newTestLocker()
	boom := errors.New("the report has no rows")

	if err := locker.Run(context.Background(), "reports.weekly", time.Minute,
		func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("the work's error came back as %v", err)
	}

	ran := false
	if err := locker.Run(context.Background(), "reports.weekly", time.Minute,
		func(context.Context) error { ran = true; return nil }); err != nil {
		t.Fatalf("after a failure: %v", err)
	}
	if !ran {
		t.Error("the lock was not given back after the work failed")
	}
}

// TestTheNameIsTheWholeIdentity: the name is used as given and nothing is added
// to it. Two names are two locks, which is what lets a scheduler run two
// different tasks at the same minute.
//
// It is also the other half of the name not being scoped: if anything were
// mixed into the key, one of these two assertions would fail.
func TestTheNameIsTheWholeIdentity(t *testing.T) {
	locker := newTestLocker()

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = locker.Run(context.Background(), "reports.monthly", time.Minute,
			func(context.Context) error {
				close(holding)
				<-release
				return nil
			})
	}()
	<-holding

	ran := false
	if err := locker.Run(context.Background(), "invoices.sweep", time.Minute,
		func(context.Context) error { ran = true; return nil }); err != nil {
		t.Fatalf("a different name was refused: %v", err)
	}
	if !ran {
		t.Error("a task under a different name did not run while another lock was held")
	}

	close(release)
	<-done
}
