// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/hesape, against the
// code that now runs; the unit tests that used to live here were tests of an
// implementation this package no longer holds, and keeping a second copy of
// them would be a second place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- and one round trip
// per envelope, because an envelope is the place a rename can be wired to the
// wrong method and still compile.

package security_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/encryption"
	"github.com/arandu-io/hesape/hashing"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

var appKey = []byte("0123456789abcdef0123456789abcdef")

// TestAliasesAreTheHesapeSymbols is the whole of the alias half of this bridge.
//
// Every line is a compile-time assertion that the two names are one type or one
// value. A rename in hesape that this package has not followed fails here
// rather than in the thirteen repositories that import these names.
func TestAliasesAreTheHesapeSymbols(t *testing.T) {
	var (
		_ auth.Subject   = security.Subject{}
		_ security.Grant = auth.Grant{}
		_ auth.Action    = security.Action("post.view")
	)
	var policy security.Policy[int] = allow{}
	var _ auth.Policy[int] = policy

	var opt session.Option = security.Remember(true)
	_ = opt
	var _ security.SessionOption = session.Remember(false)

	var _ *security.CSRF = session.NewCSRF(appKey, time.Hour)
	var _ *security.Flash = session.NewFlash(appKey, true)
	var _ *security.Signer = encryption.NewSigner(appKey)

	for name, pair := range map[string][2]error{
		"ErrForbidden":             {security.ErrForbidden, auth.ErrForbidden},
		"ErrNoSession":             {security.ErrNoSession, session.ErrNoSession},
		"ErrSessionExpired":        {security.ErrSessionExpired, session.ErrExpired},
		"ErrConfirmationNotStored": {security.ErrConfirmationNotStored, session.ErrConfirmationNotStored},
		"ErrCSRF":                  {security.ErrCSRF, session.ErrTokenMismatch},
		"ErrInvalidPassword":       {security.ErrInvalidPassword, hashing.ErrInvalidPassword},
		"ErrSignature":             {security.ErrSignature, encryption.ErrSignature},
		"ErrExpired":               {security.ErrExpired, encryption.ErrExpired},
	} {
		if pair[0] != pair[1] {
			t.Errorf("security.%s is not the hesape value: errors.Is against it answers no", name)
		}
	}

	for name, pair := range map[string][2]any{
		"SessionCookieName":          {security.SessionCookieName, session.CookieName},
		"FlashCookieName":            {security.FlashCookieName, session.FlashCookieName},
		"FlashLifetime":              {security.FlashLifetime, session.FlashLifetime},
		"MaxFlashBytes":              {security.MaxFlashBytes, session.MaxFlashBytes},
		"RememberLifetime":           {security.RememberLifetime, session.RememberLifetime},
		"PasswordConfirmationWindow": {security.PasswordConfirmationWindow, session.PasswordConfirmationWindow},
		"IntendedCookieName":         {security.IntendedCookieName, hhttp.IntendedCookieName},
		"IntendedLifetime":           {security.IntendedLifetime, hhttp.IntendedLifetime},
		"MinPasswordLen":             {security.MinPasswordLen, hashing.MinPasswordLen},
		"MaxSignInFailures":          {security.MaxSignInFailures, auth.MaxSignInFailures},
		"MaxSignInFailuresPerClient": {security.MaxSignInFailuresPerClient, auth.MaxSignInFailuresPerClient},
		"SignInWindow":               {security.SignInWindow, auth.SignInWindow},
	} {
		if pair[0] != pair[1] {
			t.Errorf("security.%s is %v, hesape says %v", name, pair[0], pair[1])
		}
	}
}

// allow is a policy that permits everything, so that Authorize is tested for
// reaching hesape rather than for deciding anything.
type allow struct{}

func (allow) Can(context.Context, security.Subject, security.Action, int) error { return nil }

// TestAuthorizeReachesHesape covers the one symbol that cannot be an alias: Go
// has no alias form for a generic function, so this is a wrapper, and a wrapper
// is a thing that can be written to call the wrong function.
func TestAuthorizeReachesHesape(t *testing.T) {
	who := security.Subject{ID: "u1", Tenant: "acme"}

	g, err := security.Authorize(context.Background(), allow{}, who, "post.view", 1)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if err := g.Check("post.view"); err != nil {
		t.Fatalf("the grant does not check against the action it was issued for: %v", err)
	}
	if got := auth.Tenant(g); got != "acme" {
		t.Errorf("hesape reads the tenant off this grant as %q, want %q", got, "acme")
	}

	if _, err := security.Authorize(context.Background(), allow{}, security.Subject{}, "post.view", 1); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("an anonymous subject was not refused with ErrForbidden: %v", err)
	}
}

// TestSystemGrantReachesHesape proves the escape hatch still refuses a tenant
// it cannot use as a namespace. It is a wrapper rather than a var precisely so
// that nothing can replace it, and a wrapper has to be shown to call through.
func TestSystemGrantReachesHesape(t *testing.T) {
	if err := security.SystemGrant("post.purge", "acme").Check("post.purge"); err != nil {
		t.Fatalf("a system grant for a valid tenant does not check: %v", err)
	}
	if err := security.SystemGrant("post.purge", "").Check("post.purge"); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a system grant with no tenant was not refused: %v", err)
	}
	if security.ValidTenant("acme/reports") {
		t.Error("a tenant carrying a separator was accepted")
	}
	if !security.Guest("acme").IsGuest() {
		t.Error("Guest does not answer IsGuest")
	}
}

// TestPasswordFunctionsReachHashing covers the three renames, and the
// round trip is what proves Make and Check were not wired to each other's
// arguments -- both take strings, so the compiler would not say.
func TestPasswordFunctionsReachHashing(t *testing.T) {
	const plain = "correct horse battery"

	encoded, err := security.HashPassword(plain)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := security.VerifyPassword(plain, encoded); err != nil {
		t.Fatalf("the hash this bridge wrote does not verify through it: %v", err)
	}
	if err := hashing.Check(plain, encoded); err != nil {
		t.Fatalf("the hash this bridge wrote does not verify in hesape: %v", err)
	}
	if err := security.VerifyPassword("wrong password entirely", encoded); !errors.Is(err, security.ErrInvalidPassword) {
		t.Errorf("a wrong password did not answer ErrInvalidPassword: %v", err)
	}
	if security.NeedsRehash(encoded) {
		t.Error("a hash just written is reported as needing a rehash")
	}
	if _, err := security.HashPassword("short"); err == nil {
		t.Error("a password under MinPasswordLen was accepted")
	}
}

// TestSignerReachesEncryption is one round trip, because the purpose is what
// the Signer is for and a bridge that dropped it would still compile.
func TestSignerReachesEncryption(t *testing.T) {
	s := security.NewSigner(appKey)

	token := s.Sign("verify.email", "u1", time.Hour)
	got, err := s.Verify("verify.email", token)
	if err != nil || got != "u1" {
		t.Fatalf("sign/verify round trip: %q, %v", got, err)
	}
	if _, err := s.Verify("password.reset", token); !errors.Is(err, security.ErrSignature) {
		t.Errorf("a token minted for one purpose verified against another: %v", err)
	}
}

// TestLocalPathReachesHesape checks the refusal that matters, because LocalPath
// is the whole of the open-redirect defence and a bridge to the wrong function
// would pass anything that starts with a slash.
func TestLocalPathReachesHesape(t *testing.T) {
	if got, ok := security.LocalPath("/invoices/44"); !ok || got != "/invoices/44" {
		t.Errorf("a local path was refused: %q, %v", got, ok)
	}
	for _, raw := range []string{"https://evil.example/login", "//evil.example/x", "/\\evil.example/x", "/a b"} {
		if _, ok := security.LocalPath(raw); ok {
			t.Errorf("LocalPath accepted %q", raw)
		}
	}
}

// TestMemoryBackendKeepsTheOldMethodNames is the assertion that
// github.com/arandu-io/kv depends on and that go build in this module cannot
// make: kv is a separate module implementing SessionBackend by these four
// names, so SessionBackend may not become an alias for hesape's Handler.
func TestMemoryBackendKeepsTheOldMethodNames(t *testing.T) {
	ctx := context.Background()
	var b security.SessionBackend = security.NewMemoryBackend()

	who := security.Subject{ID: "u1", Tenant: "acme", Roles: []string{"author"}, Verified: true}
	if err := b.Put(ctx, "sid-1", who, time.Hour); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := b.Get(ctx, "sid-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != who.ID || got.Tenant != who.Tenant || !got.Verified || !got.HasRole("author") {
		t.Errorf("the subject did not survive the round trip: %+v", got)
	}

	if _, err := b.Get(ctx, "no-such-id"); !errors.Is(err, security.ErrSessionExpired) {
		t.Errorf("an unknown id did not answer ErrSessionExpired: %v", err)
	}

	// The refusal that keeps a bulk sign-out from reaching every guest of a
	// tenant, and every tenant at once.
	if err := b.DeleteSubject(ctx, "acme", "", ""); err == nil {
		t.Error("signing out the empty subject of a tenant was accepted")
	}
	if err := b.DeleteSubject(ctx, "", "u1", ""); err == nil {
		t.Error("signing out a subject with no tenant was accepted")
	}

	if err := b.Put(ctx, "sid-2", who, time.Hour); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := b.DeleteSubject(ctx, "acme", "u1", "sid-1"); err != nil {
		t.Fatalf("delete subject: %v", err)
	}
	if _, err := b.Get(ctx, "sid-1"); err != nil {
		t.Errorf("the kept session was destroyed: %v", err)
	}
	if _, err := b.Get(ctx, "sid-2"); !errors.Is(err, security.ErrSessionExpired) {
		t.Errorf("the other session survived a bulk sign-out: %v", err)
	}

	if err := b.Delete(ctx, "sid-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := b.Get(ctx, "sid-1"); !errors.Is(err, security.ErrSessionExpired) {
		t.Errorf("a deleted session still reads back: %v", err)
	}
}

// TestSessionStoreReachesTheRenamedMethods walks one session through the whole
// envelope: Start, Load, Rotate, Confirm and Destroy are each a different name
// in hesape, and each one wired to the wrong one would still compile.
func TestSessionStoreReachesTheRenamedMethods(t *testing.T) {
	ctx := context.Background()
	store := security.NewSessionStore(appKey, time.Hour, false, nil)
	who := security.Subject{ID: "u1", Tenant: "acme"}

	w := httptest.NewRecorder()
	id, err := store.Start(ctx, w, who)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	signedIn := carry(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := store.IDFromRequest(signedIn); got != id {
		t.Errorf("IDFromRequest answered %q for the session it just wrote (%q)", got, id)
	}
	got, err := store.Load(ctx, signedIn)
	if err != nil || got.ID != "u1" || got.Tenant != "acme" {
		t.Fatalf("load: %+v, %v", got, err)
	}
	if security.PasswordConfirmedWithin(got, time.Hour) {
		t.Error("a session nobody has confirmed a password on reads as confirmed")
	}

	// Confirm writes the stamp, and Load reads it back off the payload -- which
	// is where a caller of this package has always found it, and where the
	// hesape record does not put it.
	w = httptest.NewRecorder()
	if err := store.Confirm(ctx, w, signedIn); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	got, err = store.Load(ctx, signedIn)
	if err != nil {
		t.Fatalf("load after confirm: %v", err)
	}
	if !security.PasswordConfirmedWithin(got, time.Hour) {
		t.Error("the password confirmation did not survive the record translation")
	}

	// Rotate mints a new id and ends the old one: session fixation is the bug
	// this call exists to close, so the old id has to stop working.
	w = httptest.NewRecorder()
	rotated, err := store.Rotate(ctx, w, id, who)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == id {
		t.Fatal("Rotate returned the id it was told to replace")
	}
	if _, err := store.Load(ctx, signedIn); !errors.Is(err, security.ErrSessionExpired) {
		t.Errorf("the pre-rotation session still loads: %v", err)
	}

	afterRotate := carry(w, httptest.NewRequest(http.MethodGet, "/", nil))
	w = httptest.NewRecorder()
	if err := store.Destroy(ctx, w, rotated); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := store.Load(ctx, afterRotate); !errors.Is(err, security.ErrSessionExpired) {
		t.Errorf("a destroyed session still loads: %v", err)
	}
	// Destroy clears two cookies, and the second one is the half that moved to
	// hesape/http: an address remembered before a sign-out is one the next
	// person at a shared machine must not be carried to.
	if !clears(w, security.SessionCookieName) {
		t.Error("Destroy did not clear the session cookie")
	}
	if !clears(w, security.IntendedCookieName) {
		t.Error("Destroy did not clear the intended destination")
	}
}

// TestRememberIsCarriedThrough proves the option reaches hesape's Start rather
// than being accepted and dropped: the checkbox on the published sign-in screen
// was decoration for exactly that reason once before.
func TestRememberIsCarriedThrough(t *testing.T) {
	ctx := context.Background()
	store := security.NewSessionStore(appKey, time.Hour, false, nil)

	w := httptest.NewRecorder()
	if _, err := store.Start(ctx, w, security.Subject{ID: "u1", Tenant: "acme"}, security.Remember(true)); err != nil {
		t.Fatalf("start: %v", err)
	}

	got, err := store.Load(ctx, carry(w, httptest.NewRequest(http.MethodGet, "/", nil)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Remembered {
		t.Error("Remember(true) did not reach the record")
	}
	if c := cookie(w, security.SessionCookieName); c == nil || time.Duration(c.MaxAge)*time.Second < security.RememberLifetime {
		t.Errorf("the cookie was not written for RememberLifetime: %v", c)
	}
}

// TestIntendedIsCarriedThrough covers the two methods that left the session
// package altogether. The address has to survive from the guard that writes it
// to the sign-in handler that spends it, and be spent exactly once.
func TestIntendedIsCarriedThrough(t *testing.T) {
	store := security.NewSessionStore(appKey, time.Hour, false, nil)

	w := httptest.NewRecorder()
	store.RememberIntended(w, httptest.NewRequest(http.MethodGet, "/invoices/44", nil))
	refused := carry(w, httptest.NewRequest(http.MethodGet, "/login", nil))

	w = httptest.NewRecorder()
	if got := store.TakeIntended(w, refused, "/"); got != "/invoices/44" {
		t.Errorf("the intended address came back as %q", got)
	}
	if !clears(w, security.IntendedCookieName) {
		t.Error("TakeIntended did not spend the address")
	}
	if got := store.TakeIntended(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/login", nil), "/home"); got != "/home" {
		t.Errorf("with no cookie the fallback was not answered: %q", got)
	}
}

// TestThrottleKeepsTheContextFreeSignature is the other interface that may not
// become an alias: hesape/auth added a context.Context to all three methods,
// and every caller in the framework and in the repositories that import it is
// written without one.
func TestThrottleKeepsTheContextFreeSignature(t *testing.T) {
	var th security.SignInThrottle = security.NewMemoryThrottle()

	for i := range security.MaxSignInFailures {
		if _, ok := th.Attempt("acme", "someone@example.com", "203.0.113.7"); !ok {
			t.Fatalf("attempt %d of the budget was refused", i+1)
		}
	}
	retry, ok := th.Attempt("acme", "someone@example.com", "203.0.113.7")
	if ok {
		t.Fatal("the attempt after the budget was spent was allowed")
	}
	if retry <= 0 || retry > security.SignInWindow {
		t.Errorf("retryAfter is %v, which is outside the window", retry)
	}

	th.Clear("acme", "someone@example.com", "203.0.113.7")
	if _, ok := th.Attempt("acme", "someone@example.com", "203.0.113.7"); !ok {
		t.Error("a successful sign-in did not forget the identity's failures")
	}
	th.Refund("acme", "someone@example.com", "203.0.113.7")
}

// carry copies the cookies a response wrote onto the next request, which is
// what a browser does and what these tests need in one line.
func carry(w *httptest.ResponseRecorder, r *http.Request) *http.Request {
	for _, c := range w.Result().Cookies() {
		if c.MaxAge >= 0 {
			r.AddCookie(c)
		}
	}
	return r
}

func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func clears(w *httptest.ResponseRecorder, name string) bool {
	c := cookie(w, name)
	return c != nil && c.MaxAge < 0
}
