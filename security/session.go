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
)

// SessionBackend stores the subject behind a session id.
//
// The core ships MemoryBackend only, which is enough for development and for a
// single instance. The redis adapter provides the distributed store with active
// invalidation; see docs/05-repositorios.md.
type SessionBackend interface {
	Get(ctx context.Context, id string) (Subject, error)
	Put(ctx context.Context, id string, s Subject, ttl time.Duration) error
	Delete(ctx context.Context, id string) error
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
}

// NewSessionStore returns a store. Pass secure=false only in development:
// without the Secure attribute the cookie travels over plain HTTP.
func NewSessionStore(appKey []byte, ttl time.Duration, secure bool, b SessionBackend) *SessionStore {
	if b == nil {
		b = NewMemoryBackend()
	}
	return &SessionStore{appKey: appKey, ttl: ttl, secure: secure, backend: b}
}

// Start creates a session for the subject and writes the cookie.
func (s *SessionStore) Start(ctx context.Context, w http.ResponseWriter, sub Subject) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	if err := s.backend.Put(ctx, id, sub, s.ttl); err != nil {
		return "", err
	}
	s.writeCookie(w, id)
	return id, nil
}

// Rotate issues a new session id for the same subject and destroys the old one.
//
// It MUST be called on login: keeping the pre-login id is session fixation, the
// bug that lets an attacker plant a known id and inherit the session after the
// victim authenticates. `aru doctor` checks for this call.
func (s *SessionStore) Rotate(ctx context.Context, w http.ResponseWriter, oldID string, sub Subject) (string, error) {
	id, err := s.Start(ctx, w, sub)
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

// Destroy removes the session and clears the cookie.
func (s *SessionStore) Destroy(ctx context.Context, w http.ResponseWriter, id string) error {
	if id != "" {
		if err := s.backend.Delete(ctx, id); err != nil {
			return err
		}
	}
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

func (s *SessionStore) writeCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id + "." + s.sign(id),
		Path:     "/",
		MaxAge:   int(s.ttl.Seconds()),
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
