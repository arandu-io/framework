package security_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
)

func newStore(secure bool) *security.SessionStore {
	return security.NewSessionStore(appKey, time.Hour, secure, security.NewMemoryBackend())
}

// requestWithCookies replays the cookies a response set, which is how the
// browser round trip is simulated without a browser.
func requestWithCookies(rec *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSessionStartAndLoad(t *testing.T) {
	store := newStore(true)
	subject := security.Subject{ID: "u1", Tenant: "t1", Roles: []string{"admin"}}
	rec := httptest.NewRecorder()

	id, err := store.Start(context.Background(), rec, subject)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatal("Start returned an empty session id")
	}

	got, err := store.Load(context.Background(), requestWithCookies(rec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != subject.ID || got.Tenant != subject.Tenant || !got.HasRole("admin") {
		t.Fatalf("subject = %+v, want %+v", got, subject)
	}
}

// TestSessionCookieIsHardened checks the three attributes that decide whether a
// stolen cookie is useful: HttpOnly keeps script away from it, Secure keeps it
// off plain HTTP, SameSite blunts cross-site submission.
func TestSessionCookieIsHardened(t *testing.T) {
	store := newStore(true)
	rec := httptest.NewRecorder()

	if _, err := store.Start(context.Background(), rec, security.Subject{ID: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != security.SessionCookieName {
		t.Fatalf("cookie name = %q, want %q", c.Name, security.SessionCookieName)
	}
	if !c.HttpOnly {
		t.Error("the session cookie must be HttpOnly")
	}
	if !c.Secure {
		t.Error("the session cookie must be Secure outside development")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
}

// TestSessionCookieIsSigned is what stops a forged cookie from ever reaching the
// backend: the id is only accepted with a valid HMAC.
func TestSessionCookieIsSigned(t *testing.T) {
	store := newStore(true)
	rec := httptest.NewRecorder()
	if _, err := store.Start(context.Background(), rec, security.Subject{ID: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	value := rec.Result().Cookies()[0].Value

	id, _, ok := strings.Cut(value, ".")
	if !ok {
		t.Fatalf("cookie value has no signature: %q", value)
	}

	forged := httptest.NewRequest(http.MethodGet, "/", nil)
	forged.AddCookie(&http.Cookie{Name: security.SessionCookieName, Value: id + ".not-a-signature"})

	if got := store.IDFromRequest(forged); got != "" {
		t.Fatalf("IDFromRequest accepted a forged signature: %q", got)
	}
	if _, err := store.Load(context.Background(), forged); !errors.Is(err, security.ErrNoSession) {
		t.Fatalf("error = %v, want ErrNoSession", err)
	}
}

// TestRotateInvalidatesTheOldSession is the anti session fixation guarantee: the
// pre-login id must stop working the moment the user authenticates.
func TestRotateInvalidatesTheOldSession(t *testing.T) {
	store := newStore(true)
	ctx := context.Background()

	first := httptest.NewRecorder()
	oldID, err := store.Start(ctx, first, security.Subject{ID: "anonymous-visitor"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	oldRequest := requestWithCookies(first)

	second := httptest.NewRecorder()
	newID, err := store.Rotate(ctx, second, oldID, security.Subject{ID: "u1", Tenant: "t1"})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	if newID == oldID {
		t.Fatal("Rotate reused the session id: that is session fixation")
	}
	if _, err := store.Load(ctx, oldRequest); !errors.Is(err, security.ErrSessionExpired) {
		t.Fatalf("the old session still loads: error = %v, want ErrSessionExpired", err)
	}
	if got, err := store.Load(ctx, requestWithCookies(second)); err != nil || got.ID != "u1" {
		t.Fatalf("new session: subject = %+v, error = %v", got, err)
	}
}

func TestDestroyClearsSessionAndCookie(t *testing.T) {
	store := newStore(true)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	id, err := store.Start(ctx, rec, security.Subject{ID: "u1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	request := requestWithCookies(rec)

	out := httptest.NewRecorder()
	if err := store.Destroy(ctx, out, id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, err := store.Load(ctx, request); !errors.Is(err, security.ErrSessionExpired) {
		t.Fatalf("the session survived Destroy: error = %v", err)
	}
	cleared := out.Result().Cookies()[0]
	if cleared.MaxAge >= 0 {
		t.Fatalf("MaxAge = %d, want a negative value to delete the cookie", cleared.MaxAge)
	}
}

func TestLoadWithoutCookie(t *testing.T) {
	store := newStore(true)

	_, err := store.Load(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(err, security.ErrNoSession) {
		t.Fatalf("error = %v, want ErrNoSession", err)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	store := security.NewSessionStore(appKey, -time.Second, true, security.NewMemoryBackend())
	rec := httptest.NewRecorder()
	if _, err := store.Start(context.Background(), rec, security.Subject{ID: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := store.Load(context.Background(), requestWithCookies(rec))
	if !errors.Is(err, security.ErrSessionExpired) {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}

func TestDevelopmentCookieIsNotSecure(t *testing.T) {
	store := newStore(false)
	rec := httptest.NewRecorder()
	if _, err := store.Start(context.Background(), rec, security.Subject{ID: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if rec.Result().Cookies()[0].Secure {
		t.Fatal("with secure=false the cookie must work over plain HTTP on localhost")
	}
}
