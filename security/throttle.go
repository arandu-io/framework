package security

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"
	"time"
)

// The sign-in policy. It is three constants and not configuration: a lockout
// somebody can widen from the environment is a lockout somebody widens the
// morning it fires, and then nobody narrows it again.
const (
	// MaxSignInFailures is how many wrong passwords one address may offer for
	// one identity inside SignInWindow.
	//
	// Five is chosen from the human side of the form. Somebody who is not sure
	// which of their three passwords this application knows needs three or four
	// tries; nobody needs a sixth inside the same minute. On the other side, it
	// turns an online guessing run against one account into five guesses a
	// minute, which is not an attack -- it is a rounding error against any
	// password worth the name.
	MaxSignInFailures = 5

	// MaxSignInFailuresPerClient is the budget of one address across every
	// identity it names, and it is what makes the constant above mean anything.
	//
	// Keyed only by identity and address, a leaked address list is still five
	// free guesses per account and the whole list gets walked. Worse, every
	// address typed opens a counter, so a script naming a million of them is a
	// million live entries -- the counter meant to stop an attack becomes the
	// way to exhaust the process.
	//
	// Five accounts' worth, and it is a cap on *this address*, so it is only as
	// narrow as RemoteAddr is specific. Behind a proxy that does not rewrite
	// RemoteAddr every request in the world shares one budget, and then this
	// constant is a cap on the whole application's sign-in form -- twenty-five
	// wrong passwords a minute across every customer. That deployment has to be
	// fixed at the proxy; see KeyByIP, which says the same thing from the other
	// end.
	MaxSignInFailuresPerClient = 5 * MaxSignInFailures

	// SignInWindow is how long a spent budget stays spent. A minute, because
	// the point is to make guessing slow rather than to punish: the person who
	// mistyped their password five times gets in a minute later without writing
	// to support.
	//
	// It is a fixed window and not a sliding one, so the honest number is up to
	// ten guesses across a boundary -- five in the last instant of one window
	// and five in the first instant of the next -- and five a minute after that.
	// The same arithmetic as MemoryLimiter, kept deliberately: a second, cleverer
	// notion of "window" in the same framework is what RULE 9 refuses, and
	// doubling five is not what makes an online guessing run work.
	SignInWindow = time.Minute
)

// SignInThrottle counts sign-in attempts, so that a leaked password list cannot
// be tried against an account faster than a person can type.
//
// # The unit is taken before the password is checked
//
// This is the whole shape of the thing, and the first version got it wrong. It
// asked whether the account was locked out and recorded the failure afterwards,
// with an argon2 hash in between -- and between the question and the answer
// there is a tenth of a second in which nothing has been written down. Eight
// requests fired at the same instant all asked, all got "no", and all eight had
// their passwords checked: the budget was five and the burst got eight, and with
// a few hundred open sockets it would have got a few hundred. A counter that
// only counts attempts arriving one after another does not limit an attacker,
// who has no reason to wait for the previous answer.
//
// So Attempt takes the unit at the moment it decides, under one lock, and the
// two calls below give it back. A successful sign-in forgets the identity's
// failures entirely; an attempt that never got as far as testing a credential
// gives back exactly what it took. What is left counted is what was actually
// tried and actually wrong.
//
// # Why this is not middleware.Limiter
//
// A route limiter counts requests and has nothing to say about either of the
// other two calls, and those are the whole difference between a failure counter
// and a rate limit: forgetting on success is what keeps the person who finally
// remembers their password on the fifth try from being locked out by the four
// before it, and giving a unit back is what keeps a database outage from
// spending every account's budget. Widening Limiter would push both into every
// implementation of it, including the distributed one, which would mean nothing
// by them. And it could not be done anyway: httpx imports security, so security
// naming a type from httpx/middleware is an import cycle. The layering already
// decided this one.
//
// So: a second interface, and deliberately not a second mechanism. The in-memory
// implementation below is what the core ships; a kv-backed one has this same
// shape, which is what makes it an adapter rather than a mode (RULE 11).
type SignInThrottle interface {
	// Attempt records one sign-in attempt against this identity from this
	// address and reports whether it may go ahead. A refused attempt costs
	// nothing, so hammering a locked-out account does not extend the lockout.
	//
	// The tenant is part of the key: a rate limit shared across tenants is one
	// customer's traffic locking another customer's users out (RULE 14).
	Attempt(tenant, identity, client string) (retryAfter time.Duration, ok bool)

	// Refund gives back the unit Attempt took, for an attempt that never
	// reached the credential -- the users table was unreachable, the request was
	// cancelled. It gives back one unit and never clears the count, which is
	// what keeps it from being the way out of a lockout: an attempt that refunds
	// itself is worth exactly the nothing it cost.
	Refund(tenant, identity, client string)

	// Clear forgets what this identity failed from this address, and gives the
	// address back the unit this attempt took. Call it on a successful sign-in.
	//
	// The address's remaining count is deliberately left standing. One account
	// whose password the caller does know is exactly what a script walking a
	// stolen list has, and clearing the address's budget on success would let it
	// reset that budget every few tries.
	Clear(tenant, identity, client string)
}

// maxTrackedSignIns caps the table in MemoryThrottle.
//
// The per-client budget already bounds what one address can put in it, so this
// only matters against a botnet large enough that thousands of addresses are
// each spending their budget inside the same minute. At roughly a hundred and
// fifty bytes an entry it is around ten megabytes, which is a price worth
// paying to be certain the answer to "what is the worst case" is a number.
const maxTrackedSignIns = 1 << 16

// MemoryThrottle is the sign-in throttle in process memory.
//
// It is right for development and for a single instance. Behind more than one
// pod the budget multiplies by the number of pods, the same way the in-memory
// limiter's window does -- use the kv-backed implementation there.
//
// It has no background goroutine. Everything it removes, it removes on the call
// that would have grown it, because a sweeper started by a constructor outlives
// every test that builds one and keeps the whole table alive with it.
type MemoryThrottle struct {
	mu       sync.Mutex
	counters map[string]*failureCount

	// lastSweep bounds the ordinary case: without it every key ever seen stays
	// in the map until the process ends, which is a leak driven by anyone who
	// can reach the sign-in form.
	lastSweep time.Time

	// now is the clock. It is a field so that a test can prove the counter
	// opens again after the window without sleeping for a minute; nothing
	// outside this package can replace it.
	now func() time.Time
}

type failureCount struct {
	count int
	reset time.Time
}

// NewMemoryThrottle returns an empty in-memory sign-in throttle.
func NewMemoryThrottle() *MemoryThrottle {
	return &MemoryThrottle{counters: map[string]*failureCount{}, lastSweep: time.Now(), now: time.Now}
}

// Attempt takes one unit from the pair's budget and one from the address's, or
// refuses.
//
// Both budgets are read before either is written, and all of it happens under
// one lock. That is not tidiness: deciding and recording in two steps is what
// let a burst of simultaneous guesses each see an empty counter, and the lock is
// what makes the decision and the record the same event.
func (m *MemoryThrottle) Attempt(tenant, identity, client string) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	// Before the two writes, never after: the table has to be trimmed by the
	// call that grows it, or the last entry of a flood is the one that stays.
	m.trim(now)

	pk := pairKey(tenant, identity, client)
	if retry, spent := m.spent(pk, MaxSignInFailures, now); spent {
		return retry, false
	}
	// The address's own budget is read before the pair counter is opened, and
	// that ordering is what keeps a script naming a million addresses from
	// putting a million entries in the table: once MaxSignInFailuresPerClient is
	// gone, everything from that address is refused until the window turns over,
	// so a new counter would record something nobody will ever ask about.
	// Leaving it to the caller to stop calling would be leaving the memory bound
	// to whoever writes the next sign-in screen.
	ck := clientKey(tenant, client)
	if retry, spent := m.spent(ck, MaxSignInFailuresPerClient, now); spent {
		return retry, false
	}

	m.count(pk, now)
	m.count(ck, now)
	return 0, true
}

// Refund gives one unit back to each of the two budgets.
func (m *MemoryThrottle) Refund(tenant, identity, client string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.giveBack(pairKey(tenant, identity, client))
	m.giveBack(clientKey(tenant, client))
}

// Clear forgets the pair's failures and refunds the address for this attempt.
func (m *MemoryThrottle) Clear(tenant, identity, client string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counters, pairKey(tenant, identity, client))
	// One unit, not the whole count. Signing in successfully has to be free --
	// otherwise a busy office behind one address spends its shared budget on
	// people who typed their password correctly -- and it has to be no better
	// than free, or a script that owns one account on the system buys itself a
	// fresh list to walk every time it signs into it.
	m.giveBack(clientKey(tenant, client))
}

// Len reports how many counters are held. It exists so a test can prove the
// table is bounded, and it mirrors MemoryLimiter.Len for the same reason.
func (m *MemoryThrottle) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.counters)
}

// spent answers for one key. The caller holds the lock.
func (m *MemoryThrottle) spent(key string, limit int, now time.Time) (time.Duration, bool) {
	c, ok := m.counters[key]
	if !ok || !now.Before(c.reset) || c.count < limit {
		return 0, false
	}
	return c.reset.Sub(now), true
}

// count adds one to a key, starting a fresh window when the last one has run
// out. The caller holds the lock.
func (m *MemoryThrottle) count(key string, now time.Time) {
	c, ok := m.counters[key]
	if !ok || !now.Before(c.reset) {
		c = &failureCount{reset: now.Add(SignInWindow)}
		m.counters[key] = c
	}
	c.count++
}

// giveBack takes one off a key and drops it when nothing is left, so an
// application whose sign-ins all succeed holds no counters at all. The caller
// holds the lock.
func (m *MemoryThrottle) giveBack(key string) {
	c, ok := m.counters[key]
	if !ok {
		return
	}
	if c.count--; c.count <= 0 {
		delete(m.counters, key)
	}
}

// trim keeps the table bounded. The caller holds the lock.
//
// Two steps, and the second only matters when the first was not enough:
//
//  1. Once per window, drop what has expired. This is the ordinary case, and it
//     costs one pass a minute rather than a pass per sign-in.
//  2. At the cap, evict until it is under again -- two entries per call, which
//     is what one Attempt can add, so the flood pays for itself instead of
//     every sign-in walking the table. Whoever the map hands over first is
//     forgiven, which is the right way round: the alternative is refusing to
//     record new attempts, and that is an attacker who fills the table to switch
//     the lockout off for everybody.
//
// Reaching step two means tens of thousands of addresses spending their budgets
// inside the same minute, because the per-client budget in Attempt is what
// bounds each one. That is a botnet, and at that size the counters are no longer
// the interesting defence.
func (m *MemoryThrottle) trim(now time.Time) {
	if now.Sub(m.lastSweep) >= SignInWindow {
		m.dropUpTo(now)
		m.lastSweep = now
	}
	// Room for two, because one Attempt adds two counters and the cap has to
	// hold after them rather than before them.
	for k := range m.counters {
		if len(m.counters) <= maxTrackedSignIns-2 {
			return
		}
		delete(m.counters, k)
	}
}

// dropUpTo removes every counter whose window ends at or before cut.
func (m *MemoryThrottle) dropUpTo(cut time.Time) {
	for k, c := range m.counters {
		if !cut.Before(c.reset) {
			delete(m.counters, k)
		}
	}
}

func pairKey(tenant, identity, client string) string {
	return "signin:" + tenant + ":" + fingerprint(identity) + ":" + client
}

func clientKey(tenant, client string) string {
	return "client:" + tenant + ":" + client
}

// fingerprint is what goes into the key instead of the address somebody typed.
//
// Two reasons, and both are about the value being attacker-chosen. An e-mail
// address in a map key is an e-mail address in every heap dump and every
// profile of the process, in a table nobody would think to look at. And the
// form field has no length limit, so a raw key is a key whose size the client
// picks -- a million-character address typed a hundred times is a hundred
// megabytes.
//
// Lower-cased and trimmed first, because the users table is case-insensitive on
// e-mail: otherwise "Ana@example.com" and "ana@example.com" are one account with
// two budgets, and the attacker gets ten guesses instead of five.
func fingerprint(identity string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(identity))))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}
