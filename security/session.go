package security

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SessionCookieName is the cookie the framework reads and writes. It is fixed
// on purpose: a configurable cookie name buys nothing and breaks the CSRF
// binding when two parts of a project disagree about it.
const SessionCookieName = "arandu_session"

// Errors returned by SessionStore.
var (
	// ErrNoSession means the request carries no session cookie, or the cookie
	// signature does not match the application key.
	ErrNoSession = errors.New("arandu: no session")
	// ErrSessionExpired means the session id is well formed but the backend no
	// longer holds it -- expired, or destroyed by a logout elsewhere.
	ErrSessionExpired = errors.New("arandu: session expired")
	// ErrConfirmationNotStored means the backend accepted the password
	// confirmation stamp and did not keep it, so no window would ever be
	// satisfied and the password screen would ask again immediately.
	//
	// It is a defect in the backend, not in the request: the fix is to carry
	// Subject.PasswordConfirmedAt in whatever shape that backend stores. A
	// handler that receives it must report a failure rather than redirect,
	// because redirecting is the loop.
	ErrConfirmationNotStored = errors.New("arandu: the session backend did not keep the password confirmation stamp")
)

// errNoSubjectScope is the one refusal behind every bulk sign-out, stated once
// so the store and the backends cannot drift apart on it.
//
// Without an id and a tenant there is no question to ask: "every session of
// subject 1" with no tenant reaches every customer (RULE 14), and "every session
// of the empty subject" of one tenant is every session nobody has signed in on
// -- the guests. Found by audit: SessionStore.DestroyOthers refused both, and
// MemoryBackend.DeleteSubject, which is exported and reachable without the
// store, happily deleted every guest session of a tenant while the kv backend
// answered the same call with an error. Two backends that disagree is a bug that
// only appears in production, which is the only place the kv one runs.
var errNoSubjectScope = errors.New("arandu: signing out the other sessions needs a subject with an id and a tenant")

// RememberLifetime is how long a session started with Remember(true) lives.
//
// Longer than a working session, and deliberately not unlimited. The cookie is a
// bearer credential sitting on a device that gets shared, lost, resold and
// borrowed, so "stay signed in" has to end on its own: Laravel's remember cookie
// lasts five years, which means a laptop sold in year two still opens the
// account. A month is long enough for the box to be worth ticking -- that is the
// whole point of it -- and short enough that a device which left the person's
// hands stops working inside a billing cycle, where somebody notices.
//
// A store configured with a longer ttl than this keeps its own: see
// SessionStore.lifetime. Remember must never make a session shorter.
const RememberLifetime = 30 * 24 * time.Hour

// PasswordConfirmationWindow is how long typing the password again counts for.
//
// Three hours, which is what Laravel's auth.password_timeout has been since 6.x,
// and a constant for the reason MaxSignInFailures is one: a window somebody can
// widen from the environment is a window somebody widens the afternoon it is
// inconvenient, and nobody narrows it again.
//
// The number is chosen from both ends. Long enough that somebody spending an
// afternoon in the sensitive part of an application types their password once
// rather than at every step, because a check people route around is not a check.
// Short enough that a machine left unlocked overnight asks whoever sits down at
// it in the morning -- which is the situation the confirmation exists for, and
// the one a session lifetime alone never catches.
//
// It is read by middleware.RequireConfirmedPassword and by anything else asking
// Subject.PasswordConfirmedWithin, so the whole application agrees on one answer
// to "recently".
const PasswordConfirmationWindow = 3 * time.Hour

// SessionBackend stores the subject behind a session id.
//
// The core ships MemoryBackend only, which is enough for development and for a
// single instance. The redis adapter provides the distributed store with active
// invalidation; see docs/05-repositorios.md.
type SessionBackend interface {
	// Get returns the subject, or ErrSessionExpired when the backend does not
	// hold the id -- expired, evicted, or destroyed by a logout elsewhere.
	//
	// ErrSessionExpired specifically, not the backend's own not-found error.
	// Callers branch on it to send somebody back to the login page, and a
	// backend that returns something else makes swapping the store change the
	// behaviour of the application. Found by audit: the kv backend returned its
	// own kv.ErrNotFound, so an expired session in Redis fell through to the
	// generic error path that a single-instance deployment never reached.
	Get(ctx context.Context, id string) (Subject, error)
	Put(ctx context.Context, id string, s Subject, ttl time.Duration) error
	Delete(ctx context.Context, id string) error

	// DeleteSubject removes every session belonging to one subject of one tenant,
	// except keepID. An empty keepID keeps none of them.
	//
	// It is what a password change and a password reset need, and until it existed
	// they could not do it: a reset that leaves the other sessions open leaves
	// whoever forced the reset signed in on their own machine, which is the exact
	// person the reset is aimed at. Deleting by id one at a time was not an option
	// -- nothing knew which ids belonged to the account.
	//
	// The tenant is part of the question, not a filter applied afterwards (RULE
	// 14). Two tenants may both hold a subject called "1", and signing one of them
	// out must not touch the other.
	//
	// It is not an error for the subject to have no sessions. It IS an error for
	// the tenant or the subject id to be empty: neither names a subject, and an
	// implementation that treats the empty id as one signs out every session
	// nobody has signed in on. Both refusals are made here as well as in
	// SessionStore.DestroyOthers, because this interface is exported and an
	// implementation is reachable without the store.
	DeleteSubject(ctx context.Context, tenant, subjectID, keepID string) error

	// Put must store every exported field of the Subject it is given. A field it
	// silently drops reads back as the zero value, which is a decision the
	// application makes on stale information -- and only in the deployment that
	// uses that backend, never in a test against MemoryBackend. Confirm checks
	// the one field whose loss is otherwise undiagnosable; the rest are on the
	// implementation.
}

// SessionStore issues and validates sessions.
//
// The cookie value is the session id plus an HMAC of it. The signature is
// checked before the backend is touched, so a forged cookie never reaches the
// store -- and, in a distributed store, never costs a network round trip.
type SessionStore struct {
	appKey  []byte
	ttl     time.Duration
	secure  bool
	backend SessionBackend

	// signer signs the intended-destination cookie, which is the one other
	// thing this store puts in a browser. It is built once here rather than per
	// request because it is the application key with a name on it, and it lives
	// on the store rather than beside it so that RememberIntended and
	// TakeIntended are reachable from everything that already holds a store --
	// every guard and every sign-in handler -- with no second thing to wire and
	// therefore no way to wire it wrong. See intended.go.
	signer *Signer
}

// NewSessionStore returns a store. Pass secure=false only in development:
// without the Secure attribute the cookie travels over plain HTTP.
func NewSessionStore(appKey []byte, ttl time.Duration, secure bool, b SessionBackend) *SessionStore {
	if b == nil {
		b = NewMemoryBackend()
	}
	return &SessionStore{appKey: appKey, ttl: ttl, secure: secure, backend: b, signer: NewSigner(appKey)}
}

// SessionOption adjusts how a session is started.
//
// A variadic option and not a second constructor: StartFor beside Start would be
// two functions that both start a session, and the next thing anybody needs -- a
// session for a device, for an impersonation, for a longer window -- adds a
// third. One function that takes options widens; a second name forks (RULE 9).
// Every existing call to Start and Rotate passes none and behaves as it did.
type SessionOption func(*sessionSettings)

// sessionSettings is what the options add up to. Unexported, so the set of
// things a caller can ask for is the set of exported options.
type sessionSettings struct {
	remember bool
}

// Remember asks for a session that survives closing the browser, for
// RememberLifetime instead of the store's ttl.
//
// It takes the answer rather than being a flag, so the call site is the form
// field and there is no branch around it:
//
//	store.Rotate(ctx, w, old, sub, security.Remember(r.PostFormValue("remember") != ""))
//
// The sign-in screen the starter kit publishes has drawn that checkbox from the
// beginning, and nothing could read it: there was no shape in this API through
// which a longer session could be asked for, so the box was decoration in every
// project the kit created.
func Remember(on bool) SessionOption {
	return func(s *sessionSettings) { s.remember = on }
}

// lifetime is how long a session lives, and therefore both what the backend is
// told and what the cookie's MaxAge says. The two are computed here exactly once
// so they cannot disagree: a cookie that outlives its record is a session that
// looks signed in and is not, and the person sees a login screen with no
// explanation on their next click.
func (s *SessionStore) lifetime(remembered bool) time.Duration {
	if !remembered {
		return s.ttl
	}
	if s.ttl > RememberLifetime {
		// An application that already configured a longer session keeps it.
		// Otherwise ticking "remember me" shortened the session, which is the one
		// thing the box must never do.
		return s.ttl
	}
	return RememberLifetime
}

// Start creates a session for the subject and writes the cookie.
//
// With no options it is what it has always been: a session for the store's
// configured ttl. See Remember for the only thing there is to ask for.
func (s *SessionStore) Start(ctx context.Context, w http.ResponseWriter, sub Subject, opts ...SessionOption) (string, error) {
	var settings sessionSettings
	for _, opt := range opts {
		opt(&settings)
	}

	id, err := newSessionID()
	if err != nil {
		return "", err
	}

	// Both fields are written here, over whatever the caller left in the subject.
	// Remembered is the option's answer and nothing else's, and a session that has
	// just been created has never had its password confirmed on it -- inheriting a
	// stamp through Rotate would hand a fresh session the confirmation the old one
	// earned.
	sub.Remembered = settings.remember
	sub.PasswordConfirmedAt = time.Time{}

	life := s.lifetime(sub.Remembered)
	if err := s.backend.Put(ctx, id, sub, life); err != nil {
		return "", err
	}
	s.writeCookie(w, id, life)
	return id, nil
}

// Rotate issues a new session id for the same subject and destroys the old one.
//
// It MUST be called on login: keeping the pre-login id is session fixation, the
// bug that lets an attacker plant a known id and inherit the session after the
// victim authenticates. `aru doctor` checks for this call.
// The options are the same as Start's, and Rotate takes them because login is
// where remember-me is answered: the sign-in handler calls Rotate, not Start, so
// an option only Start accepted would be unreachable from the one screen that
// has the checkbox on it.
func (s *SessionStore) Rotate(ctx context.Context, w http.ResponseWriter, oldID string, sub Subject, opts ...SessionOption) (string, error) {
	id, err := s.Start(ctx, w, sub, opts...)
	if err != nil {
		return "", err
	}
	if oldID != "" && oldID != id {
		// A failure to delete the old id must not fail the login: the new
		// session is already valid and the old one expires on its own.
		_ = s.backend.Delete(ctx, oldID)
	}
	return id, nil
}

// Load returns the subject bound to the request's session cookie.
func (s *SessionStore) Load(ctx context.Context, r *http.Request) (Subject, error) {
	id := s.IDFromRequest(r)
	if id == "" {
		return Subject{}, ErrNoSession
	}
	sub, err := s.backend.Get(ctx, id)
	if err != nil {
		return Subject{}, err
	}
	return sub, nil
}

// Confirm records on the request's session that the subject has just typed
// their password again.
//
// It is the write half of a step-up check: a sensitive action asks
// Subject.PasswordConfirmedWithin, sends the person to a password screen when the
// answer is no, and calls this once they get it right. Without the stamp the only
// two designs available were asking for the password on every sensitive action,
// which people route around, and asking once and never again, which is not a
// check.
//
// It rewrites the record, and rewriting it restarts the record's clock, so the
// cookie is rewritten with the same lifetime in the same breath. The session
// therefore gets a full lifetime back -- earned by proving who is holding it,
// which is the same proof that started it.
//
// It returns ErrConfirmationNotStored when the backend accepted the stamp and
// did not keep it. A handler must report that rather than redirect: redirecting
// sends the person back to the screen they just got right.
func (s *SessionStore) Confirm(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	id := s.IDFromRequest(r)
	if id == "" {
		return ErrNoSession
	}
	sub, err := s.backend.Get(ctx, id)
	if err != nil {
		return err
	}

	sub.PasswordConfirmedAt = time.Now()
	life := s.lifetime(sub.Remembered)
	if err := s.backend.Put(ctx, id, sub, life); err != nil {
		return err
	}

	// The stamp is read back before the confirmation is reported as done, because
	// a backend that stores its own wire shape can drop a field it does not know
	// about and the write still succeeds. That failure is invisible from here and
	// not survivable by the person in front of it: the sensitive action asks
	// PasswordConfirmedWithin, gets no, sends them to the password screen, they
	// type it correctly, and land on the password screen again -- forever, with
	// nothing in the logs. Found by audit on the kv backend, whose stored shape
	// does not carry this field and cannot yet, because the field is newer than
	// the framework version that module requires.
	//
	// One extra round trip, on an operation that happens once per sensitive
	// action and never on a page load. The alternative was a loop that reads as
	// "my password is wrong".
	written, err := s.backend.Get(ctx, id)
	if err != nil {
		return err
	}
	if written.PasswordConfirmedAt.IsZero() {
		return ErrConfirmationNotStored
	}

	s.writeCookie(w, id, life)
	return nil
}

// PasswordConfirmedWithin reports whether the password was typed again on this
// session less than window ago.
//
// It answers false whenever it cannot prove otherwise, which is the whole
// argument for having it be a method rather than a comparison at the call site:
//
//   - No stamp is not confirmed. A session written by an older binary, or by a
//     backend that does not carry the field yet, has the zero time -- and the
//     reading that costs somebody one password screen is the correct one, while
//     the reading that treats an absent stamp as recent waves every session that
//     survived a deploy straight past the check.
//   - A stamp in the future is not confirmed either. It is a clock that moved or
//     a record that was tampered with, and neither is proof that a person was
//     there.
//   - A window of zero or less is not confirmed, so "no window configured" cannot
//     read as "always confirmed".
func (sub Subject) PasswordConfirmedWithin(window time.Duration) bool {
	if window <= 0 || sub.PasswordConfirmedAt.IsZero() {
		return false
	}
	elapsed := time.Since(sub.PasswordConfirmedAt)
	return elapsed >= 0 && elapsed < window
}

// DestroyOthers signs the subject out of every session except keepID.
//
// Pass the id of the session doing the asking to keep the person signed in where
// they are -- a password change from the account screen -- and pass an empty
// keepID to end all of them, which is what a password reset from an e-mail link
// wants: there is no session to keep, and the one session that must stop working
// belongs to whoever forced the reset.
//
// It does not touch the cookie. The kept session's cookie is still valid and
// every other browser is holding a cookie whose record is gone, which is a
// session that stops at the next request -- there is no way to reach into those
// browsers and no need to.
//
// The tenant comes from the subject, which came from the Grant or the session,
// never from the request (RULE 14). A subject with no tenant is refused rather
// than turned into a query that matches an id across every customer.
func (s *SessionStore) DestroyOthers(ctx context.Context, sub Subject, keepID string) error {
	if sub.ID == "" || sub.Tenant == "" {
		return errNoSubjectScope
	}
	return s.backend.DeleteSubject(ctx, sub.Tenant, sub.ID, keepID)
}

// Destroy removes the session and clears the cookies this store put in the
// browser -- the session, and the intended destination.
//
// The second one is not tidiness. Signing out is the moment a shared machine
// changes hands, and the intended address outlives it by up to
// IntendedLifetime: whoever signs in next is carried to the page the previous
// person was refused. That is somebody else's address bar handed to a stranger
// -- "/customers/98213/invoices/44" says who the last person was working on --
// and it reads to the new person as the application taking them somewhere at
// random. The guards will still refuse them anything they may not open, so what
// is closed here is the disclosure and the confusion, not an authorization hole.
//
// Every call site of Destroy in this framework and in the projects it ships is a
// sign-out, which is why the clearing is unconditional rather than an option: an
// address remembered before a sign-out is an address nobody wants afterwards.
func (s *SessionStore) Destroy(ctx context.Context, w http.ResponseWriter, id string) error {
	if id != "" {
		if err := s.backend.Delete(ctx, id); err != nil {
			return err
		}
	}
	s.clearIntended(w)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// IDFromRequest returns the session id when the cookie signature is valid, and
// the empty string otherwise. It is the function to hand to the CSRF
// middleware, which binds its token to this id.
func (s *SessionStore) IDFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	id, sig, ok := strings.Cut(c.Value, ".")
	if !ok || id == "" {
		return ""
	}
	if !hmac.Equal([]byte(s.sign(id)), []byte(sig)) {
		return ""
	}
	return id
}

// writeCookie takes the lifetime rather than reading s.ttl, because the record
// was written with that same value: a remembered session whose cookie still said
// one hour was a session the browser threw away while the backend held it, and
// the person was signed out an hour after ticking a box that promised a month.
func (s *SessionStore) writeCookie(w http.ResponseWriter, id string, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id + "." + s.sign(id),
		Path:     "/",
		MaxAge:   int(lifetime.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *SessionStore) sign(id string) string {
	m := hmac.New(sha256.New, s.appKey)
	m.Write([]byte(SessionCookieName))
	m.Write([]byte{0})
	m.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// MemoryBackend keeps sessions in the process memory.
//
// It is the right choice for development and for a single instance. Behind more
// than one pod it silently logs people out on every deploy and on every request
// routed elsewhere -- use the redis adapter there.
type MemoryBackend struct {
	mu      sync.RWMutex
	entries map[string]memorySession
}

type memorySession struct {
	subject Subject
	expires time.Time
}

// NewMemoryBackend returns an empty in-memory session backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{entries: map[string]memorySession{}}
}

// Get returns the subject, or ErrSessionExpired when the id is unknown.
func (m *MemoryBackend) Get(ctx context.Context, id string) (Subject, error) {
	m.mu.RLock()
	e, ok := m.entries[id]
	m.mu.RUnlock()
	if !ok {
		return Subject{}, ErrSessionExpired
	}
	if time.Now().After(e.expires) {
		m.mu.Lock()
		delete(m.entries, id)
		m.mu.Unlock()
		return Subject{}, ErrSessionExpired
	}
	return e.subject, nil
}

// Put stores the subject under id for the given ttl.
func (m *MemoryBackend) Put(ctx context.Context, id string, s Subject, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[id] = memorySession{subject: s, expires: time.Now().Add(ttl)}
	return nil
}

// Delete removes the session, if present.
func (m *MemoryBackend) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
	return nil
}

// DeleteSubject removes every session of one subject of one tenant except
// keepID.
//
// It is a scan of the map, and it stays a scan: a second map keyed by subject
// would have to be kept in step with expiry, with Delete and with eviction, and
// getting that wrong leaves a password reset believing it signed somebody out.
// This backend holds one instance's sessions, and walking them costs less than
// the round trip the caller just made. A distributed backend cannot scan and
// carries a real index -- see the kv adapter.
func (m *MemoryBackend) DeleteSubject(ctx context.Context, tenant, subjectID, keepID string) error {
	// The same refusal SessionStore.DestroyOthers makes, repeated here because
	// this method is exported and a caller can reach it without the store. It
	// used to loop with whatever it was given: tenant "t1" and an empty subject
	// id matched every session that had no subject on it, which is every guest,
	// and the kv backend refused the identical call. See errNoSubjectScope.
	if tenant == "" || subjectID == "" {
		return errNoSubjectScope
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.entries {
		if id == keepID {
			continue
		}
		if e.subject.Tenant != tenant || e.subject.ID != subjectID {
			continue
		}
		delete(m.entries, id)
	}
	return nil
}
