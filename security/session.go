// Sessions, answered by github.com/arandu-io/hesape/session, and -- for the
// address a guard remembers before sending somebody to the sign-in screen --
// by github.com/arandu-io/hesape/http.
//
// This is where the design diverged most, so this is where the envelopes are.
// The constants, the errors and the option type alias; SessionBackend,
// MemoryBackend and SessionStore translate.

package security

import (
	"context"
	"net/http"
	"time"

	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

// SessionCookieName is the cookie the framework reads and writes. It is fixed
// on purpose: a configurable cookie name buys nothing and breaks the CSRF
// binding when two parts of a project disagree about it.
//
// Renamed on the way to hesape: it is session.CookieName there, because the
// package name already says which cookie it is.
const SessionCookieName = session.CookieName

// Errors returned by SessionStore.
var (
	// ErrNoSession means the request carries no session cookie, or the cookie
	// signature does not match the application key.
	ErrNoSession = session.ErrNoSession

	// ErrSessionExpired means the session id is well formed but the backend no
	// longer holds it -- expired, or destroyed by a logout elsewhere.
	//
	// Renamed on the way to hesape: it is session.ErrExpired there. The alias
	// is what keeps github.com/arandu-io/kv correct without a line changing --
	// it returns this value, and hesape/session.Handler requires that one, and
	// they are the same value.
	ErrSessionExpired = session.ErrExpired

	// ErrConfirmationNotStored means the backend accepted the password
	// confirmation stamp and did not keep it, so no window would ever be
	// satisfied and the password screen would ask again immediately.
	ErrConfirmationNotStored = session.ErrConfirmationNotStored
)

// RememberLifetime is how long a session started with Remember(true) lives.
const RememberLifetime = session.RememberLifetime

// PasswordConfirmationWindow is how long typing the password again counts for.
const PasswordConfirmationWindow = session.PasswordConfirmationWindow

// SessionOption adjusts how a session is started.
//
// Renamed on the way to hesape: it is session.Option there.
type SessionOption = session.Option

// Remember asks for a session that survives closing the browser, for
// RememberLifetime instead of the store's ttl.
func Remember(on bool) SessionOption { return session.Remember(on) }

// PasswordConfirmedWithin reports whether the password was typed again on this
// session less than window ago.
//
// It was a method on Subject and it cannot be one here: Subject is an alias for
// hesape/auth.Subject, and Go does not let a package declare a method on a type
// another package owns. In hesape the question is asked of the session record
// -- session.Record.PasswordConfirmedWithin -- because that is where the stamp
// lives once the payload is opaque to the store.
//
// So this is a function, and it is the one place in this bridge where a caller
// has to be rewritten rather than recompiled:
//
//	sub.PasswordConfirmedWithin(w)  becomes  security.PasswordConfirmedWithin(sub, w)
//
// It builds the record hesape asks and asks it, so the three refusals -- no
// stamp, a stamp in the future, a window of zero -- are decided by the code
// that runs in production and not by a second copy of them here.
func PasswordConfirmedWithin(sub Subject, window time.Duration) bool {
	return recordFor(sub).PasswordConfirmedWithin(window)
}

// SessionBackend stores the subject behind a session id.
//
// It stays declared here, with the old four method names, rather than aliasing
// hesape/session.Handler -- which renamed all four: Get is Read, Put is Write,
// Delete is Destroy and DeleteSubject is DestroyIndex.
//
// github.com/arandu-io/kv implements this interface, by these names, and it is
// a separate module: an alias here would compile in the framework and break the
// adapter silently, which is the one failure this bridge exists to prevent.
// backendHandler is what carries an implementation of this across to hesape.
type SessionBackend interface {
	// Get returns the subject, or ErrSessionExpired when the backend does not
	// hold the id -- expired, evicted, or destroyed by a logout elsewhere.
	Get(ctx context.Context, id string) (Subject, error)

	// Put stores the subject under id for the given ttl. It must store every
	// exported field of the Subject it is given: a field it silently drops
	// reads back as the zero value, and only in the deployment that uses that
	// backend.
	Put(ctx context.Context, id string, s Subject, ttl time.Duration) error

	// Delete removes the session, if present.
	Delete(ctx context.Context, id string) error

	// DeleteSubject removes every session belonging to one subject of one
	// tenant, except keepID. An empty keepID keeps none of them.
	//
	// The tenant is part of the question, not a filter applied afterwards.
	// An empty tenant or an empty subject id is an error.
	DeleteSubject(ctx context.Context, tenant, subjectID, keepID string) error
}

// recordFor is half the translation SessionStore exists to do: hesape/session
// keeps the index -- the tenant, the account and the two fields that decide how
// long the record lives -- beside an opaque payload, where this package kept
// all of it on the Subject.
func recordFor(sub Subject) session.Record[Subject] {
	return session.Record[Subject]{
		Payload:             sub,
		Tenant:              sub.Tenant,
		SubjectID:           sub.ID,
		Remembered:          sub.Remembered,
		PasswordConfirmedAt: sub.PasswordConfirmedAt,
	}
}

// subjectFrom is the other half. The two fields the store owns are folded back
// onto the payload, because that is where a caller of Load has always read them
// and where a backend written against SessionBackend has always stored them.
func subjectFrom(rec session.Record[Subject]) Subject {
	sub := rec.Payload
	sub.Remembered = rec.Remembered
	sub.PasswordConfirmedAt = rec.PasswordConfirmedAt
	return sub
}

// backendHandler presents a SessionBackend as a hesape/session.Handler.
//
// It is four renames and the record translation, and it is what lets the kv
// adapter keep the method names it was written with.
type backendHandler struct{ backend SessionBackend }

var _ session.Handler[Subject] = backendHandler{}

func (h backendHandler) Read(ctx context.Context, id string) (session.Record[Subject], error) {
	sub, err := h.backend.Get(ctx, id)
	if err != nil {
		return session.Record[Subject]{}, err
	}
	return recordFor(sub), nil
}

func (h backendHandler) Write(ctx context.Context, id string, rec session.Record[Subject], ttl time.Duration) error {
	return h.backend.Put(ctx, id, subjectFrom(rec), ttl)
}

func (h backendHandler) Destroy(ctx context.Context, id string) error {
	return h.backend.Delete(ctx, id)
}

func (h backendHandler) DestroyIndex(ctx context.Context, tenant, subjectID, keepID string) error {
	return h.backend.DeleteSubject(ctx, tenant, subjectID, keepID)
}

// MemoryBackend keeps sessions in the process memory.
//
// It is the right choice for development and for a single instance. Behind more
// than one pod it silently logs people out on every deploy and on every request
// routed elsewhere -- use the kv adapter there.
//
// It is the same four renames as backendHandler, run the other way: the store
// underneath is hesape/session.ArrayHandler, and nothing is kept here.
type MemoryBackend struct {
	handler *session.ArrayHandler[Subject]
}

var _ SessionBackend = (*MemoryBackend)(nil)

// NewMemoryBackend returns an empty in-memory session backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{handler: session.NewArrayHandler[Subject]()}
}

// Get returns the subject, or ErrSessionExpired when the id is unknown.
func (m *MemoryBackend) Get(ctx context.Context, id string) (Subject, error) {
	rec, err := m.handler.Read(ctx, id)
	if err != nil {
		return Subject{}, err
	}
	return subjectFrom(rec), nil
}

// Put stores the subject under id for the given ttl.
func (m *MemoryBackend) Put(ctx context.Context, id string, s Subject, ttl time.Duration) error {
	return m.handler.Write(ctx, id, recordFor(s), ttl)
}

// Delete removes the session, if present.
func (m *MemoryBackend) Delete(ctx context.Context, id string) error {
	return m.handler.Destroy(ctx, id)
}

// DeleteSubject removes every session of one subject of one tenant except
// keepID. An empty tenant or an empty subject id is refused.
func (m *MemoryBackend) DeleteSubject(ctx context.Context, tenant, subjectID, keepID string) error {
	return m.handler.DestroyIndex(ctx, tenant, subjectID, keepID)
}

// SessionStore issues and validates sessions.
//
// It is an envelope over hesape/session.RecordStore[Subject] and
// hesape/http.Intended, because the design diverged in three ways at once:
// four methods were renamed, Load's return type changed from a Subject to a
// Record that wraps one, and the intended destination left the session package
// altogether -- it is an address, and hesape/session never validates a URL.
//
// The cookie is unchanged. hesape/session signs the same name with the same
// key, so a browser holding a session issued by the old code is still signed in.
type SessionStore struct {
	store    *session.RecordStore[Subject]
	intended *hhttp.Intended
}

// NewSessionStore returns a store. Pass secure=false only in development:
// without the Secure attribute the cookie travels over plain HTTP.
func NewSessionStore(appKey []byte, ttl time.Duration, secure bool, b SessionBackend) *SessionStore {
	if b == nil {
		b = NewMemoryBackend()
	}
	return &SessionStore{
		store:    session.NewRecordStore[Subject](appKey, ttl, secure, backendHandler{backend: b}),
		intended: hhttp.NewIntended(appKey, secure),
	}
}

// Start creates a session for the subject and writes the cookie.
func (s *SessionStore) Start(ctx context.Context, w http.ResponseWriter, sub Subject, opts ...SessionOption) (string, error) {
	return s.store.Start(ctx, w, recordFor(sub), opts...)
}

// Rotate issues a new session id for the same subject and destroys the old one.
//
// It MUST be called on login: keeping the pre-login id is session fixation.
// Renamed on the way to hesape: it is RecordStore.Regenerate there.
func (s *SessionStore) Rotate(ctx context.Context, w http.ResponseWriter, oldID string, sub Subject, opts ...SessionOption) (string, error) {
	return s.store.Regenerate(ctx, w, oldID, recordFor(sub), opts...)
}

// Load returns the subject bound to the request's session cookie.
//
// Renamed on the way to hesape, and its return type changed with it:
// RecordStore.All answers a Record that wraps the payload with the tenant, the
// account and the two fields that decide the lifetime. This unwraps it.
func (s *SessionStore) Load(ctx context.Context, r *http.Request) (Subject, error) {
	rec, err := s.store.All(ctx, r)
	if err != nil {
		return Subject{}, err
	}
	return subjectFrom(rec), nil
}

// Confirm records on the request's session that the subject has just typed
// their password again.
//
// It returns ErrConfirmationNotStored when the backend accepted the stamp and
// did not keep it. A handler must report that rather than redirect: redirecting
// sends the person back to the screen they just got right.
func (s *SessionStore) Confirm(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	return s.store.Confirm(ctx, w, r)
}

// DestroyOthers signs the subject out of every session except keepID.
//
// The tenant comes from the subject, which came from the Grant or the session,
// never from the request. A subject with no tenant is refused.
func (s *SessionStore) DestroyOthers(ctx context.Context, sub Subject, keepID string) error {
	return s.store.DestroyOthers(ctx, recordFor(sub), keepID)
}

// Destroy removes the session and clears the cookies this store put in the
// browser -- the session, and the intended destination.
//
// The second one is not tidiness. Signing out is the moment a shared machine
// changes hands, and the intended address outlives it by up to
// IntendedLifetime: whoever signs in next is carried to the page the previous
// person was refused.
//
// The two halves are two calls now, because the address moved to hesape/http:
// RecordStore.Invalidate ends the session and hhttp.Intended.Clear drops the
// address. Keeping them together here is what stops the second one from being
// forgotten at each call site.
//
// Renamed on the way to hesape: it is RecordStore.Invalidate there.
func (s *SessionStore) Destroy(ctx context.Context, w http.ResponseWriter, id string) error {
	if err := s.store.Invalidate(ctx, w, id); err != nil {
		return err
	}
	s.intended.Clear(w)
	return nil
}

// IDFromRequest returns the session id when the cookie signature is valid, and
// the empty string otherwise. It is the function to hand to the CSRF
// middleware, which binds its token to this id.
//
// Renamed on the way to hesape: it is RecordStore.ID there.
func (s *SessionStore) IDFromRequest(r *http.Request) string { return s.store.ID(r) }

// RememberIntended records where this request was going, so that the sign-in
// screen it is about to be sent to can finish the journey.
//
// Moved on the way to hesape: it is hhttp.Intended.Remember there, wired once
// at boot beside the store rather than hanging off it. This store builds one
// over the same application key, so the two are interchangeable in a browser.
func (s *SessionStore) RememberIntended(w http.ResponseWriter, r *http.Request) {
	s.intended.Remember(w, r)
}

// TakeIntended returns the address RememberIntended stored, and clears it.
//
// Moved on the way to hesape: it is hhttp.Intended.Take there.
func (s *SessionStore) TakeIntended(w http.ResponseWriter, r *http.Request, fallback string) string {
	return s.intended.Take(w, r, fallback)
}
