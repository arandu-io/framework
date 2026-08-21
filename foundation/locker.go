// The one thing in the framework that produces a Locker.
//
// The interface is declared in module.go, next to the rest of the composition
// vocabulary, because two things take one. What is here is the three lines that
// turn an issuer of locks into that shape, and the guarantees those three lines
// carry.

package foundation

import (
	"context"
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
// Run gives back what fn returned. A lock somebody else already holds is not an
// error to report: fn does not run, and the error says the lock is held, which
// for a scheduled task means another replica is doing it. The scheduler
// recognizes that answer and stays quiet.
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
// A replica that dies holding a lock releases it when the ttl expires, and
// nothing else releases it. Size it above the longest run of the work it
// guards, or a second replica starts while the first is still going.
func NewLocker(locks *cache.Locks) Locker { return issuedLocker{locks: locks} }

// issuedLocker runs work under a named lock taken from an issuer.
//
// A value rather than a pointer: it holds one field it never writes, and every
// lock is a fresh handle minted inside Run. Two goroutines calling Run share
// nothing but the issuer, which is what makes the refusal come from the store
// rather than from a race here.
type issuedLocker struct{ locks *cache.Locks }

var _ Locker = issuedLocker{}

// Run acquires the named lock, runs fn, and releases it.
//
// The handle is minted per call, so each caller carries its own proof of
// ownership and a release can only ever give back the lock it took.
func (l issuedLocker) Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error {
	return l.locks.Lock(name, ttl).Run(ctx, fn)
}
