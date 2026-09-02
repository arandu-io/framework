// The one thing in the framework that produces a Locker.
//
// The interface is declared in module.go, next to the rest of the composition
// vocabulary, because two things take one. What is here turns an issuer of
// locks into both the transient shape existing consumers use and the durable
// occurrence capability the scheduler discovers dynamically.

package foundation

import (
	"context"
	"errors"
	"time"

	"github.com/arandu-io/hesape/cache"
)

// NewLocker returns a Locker that takes each lock from locks.
//
// It is what makes a Singleton task and the outbox relay run on exactly one
// replica. Without it, N replicas each run every scheduled task on the minute
// and publish every event N times.
//
//	sched.Locker = foundation.NewLocker(cache.NewLocks(store))
//
// The store behind the issuer is what the lock is shared through, so it has to
// be one every replica reaches. A store local to the process makes every
// replica win its own lock, which is the state this exists to leave.
//
// # What a caller sees
//
// Run gives back what fn returned and releases its lock when fn finishes. A lock
// somebody else already holds is returned as the lock issuer's typed error.
//
// The returned value also satisfies [OccurrenceClaimer]. ClaimOccurrence
// reports acquisition separately from failure and deliberately keeps a claim
// until its ttl. NewLocker still returns Locker so adding that capability does
// not widen the transient contract or break custom implementations.
//
// # The name is used as given
//
// It is not scoped to a tenant, and that is the point rather than an omission.
// A lock per tenant would let every replica take a different one and run the
// same task, once per tenant -- which is the duplicate work this prevents,
// arriving under a different name. A task that reads one customer's rows gets
// its Grant from the expansion, not from the lock.
//
// # The ttl is the only way out
//
// A Run lock is released after its function and uses the ttl as crash recovery.
// An occurrence claim is never released explicitly; expiry is its bounded way
// out, including after success, failure and cancellation.
func NewLocker(locks *cache.Locks) Locker { return issuedLocker{locks: locks} }

// issuedLocker runs work under a named lock taken from an issuer.
//
// A value rather than a pointer: it holds one field it never writes, and every
// lock is a fresh handle minted inside each method. Two goroutines share
// nothing but the issuer, which is what makes the outcome come atomically from
// the store rather than from a race here.
type issuedLocker struct{ locks *cache.Locks }

var (
	_ Locker            = issuedLocker{}
	_ OccurrenceClaimer = issuedLocker{}
)

// Run acquires the named lock, runs fn, and releases it.
//
// The handle is minted per call, so each caller carries its own proof of
// ownership and a release can only ever give back the lock it took.
func (l issuedLocker) Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error {
	return l.locks.Lock(name, ttl).Run(ctx, fn)
}

// ClaimOccurrence acquires a named claim and leaves it in the store until its
// ttl expires.
//
// A held claim is an ordinary outcome, distinct from a store failure. The lock
// handle is deliberately not released: its expiry is the boundary of the
// occurrence, not the duration of the work that first claimed it.
func (l issuedLocker) ClaimOccurrence(ctx context.Context, name string, ttl time.Duration) (OccurrenceClaimOutcome, error) {
	err := l.locks.Lock(name, ttl).Acquire(ctx)
	switch {
	case err == nil:
		return OccurrenceClaimAcquired, nil
	case errors.Is(err, cache.ErrLocked):
		return OccurrenceAlreadyClaimed, nil
	default:
		return 0, err
	}
}
