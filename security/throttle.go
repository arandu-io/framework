// The sign-in throttle, answered by github.com/arandu-io/hesape/auth.
//
// The counter that stops a leaked password list from being tried against an
// account belongs next to the decision it protects, which is why it went to
// auth and not to a route limiter.

package security

import (
	"context"
	"time"

	"github.com/arandu-io/hesape/auth"
)

// SignInThrottle counts sign-in attempts, so that a leaked password list cannot
// be tried against an account faster than a person can type.
//
// The unit is taken before the password is checked, in one indivisible step,
// and the two calls below give it back. Reading a counter and writing it back
// in two round trips reopens the hole this closes; the full argument is on
// hesape/auth.SignInThrottle.
//
// It stays declared here rather than aliasing hesape/auth.SignInThrottle,
// which added a context.Context as the first parameter of all three methods.
// An alias would change the shape every caller in the framework and in the
// thirteen repositories that import it is written against, and a bridge that
// changes a signature is not a bridge.
type SignInThrottle interface {
	// Attempt records one sign-in attempt against this identity from this
	// address and reports whether it may go ahead. A refused attempt costs
	// nothing, so hammering a locked-out account does not extend the lockout.
	//
	// The tenant is part of the key: a rate limit shared across tenants is one
	// customer's traffic locking another customer's users out.
	Attempt(tenant, identity, client string) (retryAfter time.Duration, ok bool)

	// Refund gives back the unit Attempt took, for an attempt that never
	// reached the credential. It gives back one unit and never clears the
	// count, which is what keeps it from being the way out of a lockout.
	Refund(tenant, identity, client string)

	// Clear forgets what this identity failed from this address, and gives the
	// address back the unit this attempt took. Call it on a successful sign-in.
	//
	// The address's remaining count is deliberately left standing: one account
	// whose password the caller does know is exactly what a script walking a
	// stolen list has.
	Clear(tenant, identity, client string)
}

// MemoryThrottle is the sign-in throttle in process memory.
//
// It is right for development and for a single instance. Behind more than one
// pod the budget multiplies by the number of pods -- use the kv-backed
// implementation there.
//
// It is an envelope over hesape/auth.MemoryThrottle, which is the code that
// counts. What this adds is the context the old three methods do not take: the
// hesape implementation ignores it, because nothing in it blocks, so the one
// passed here is Background and the behaviour is unchanged.
type MemoryThrottle struct {
	inner *auth.MemoryThrottle
}

var _ SignInThrottle = (*MemoryThrottle)(nil)

// NewMemoryThrottle returns an empty in-memory sign-in throttle.
func NewMemoryThrottle() *MemoryThrottle {
	return &MemoryThrottle{inner: auth.NewMemoryThrottle()}
}

// Attempt takes one unit from the pair's budget and one from the address's, and
// reports whether the attempt may go ahead.
func (m *MemoryThrottle) Attempt(tenant, identity, client string) (time.Duration, bool) {
	return m.inner.Attempt(context.Background(), tenant, identity, client)
}

// Refund gives one unit back to each of the two budgets.
func (m *MemoryThrottle) Refund(tenant, identity, client string) {
	m.inner.Refund(context.Background(), tenant, identity, client)
}

// Clear forgets the pair's failures and refunds the address for this attempt.
func (m *MemoryThrottle) Clear(tenant, identity, client string) {
	m.inner.Clear(context.Background(), tenant, identity, client)
}

// Len reports how many counters are held. It exists so a test can prove the
// table does not grow without bound.
func (m *MemoryThrottle) Len() int { return m.inner.Len() }
