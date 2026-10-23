package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	twofactor "github.com/arandu-io/hesape/2fa"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/auth/account"
	"github.com/arandu-io/hesape/encryption"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

// SessionAccounts is what the sign-in screens ask of the application's
// accounts, the method set of *account.Users they call. The value SessionMatrix
// hands over implements it, and so is accepted wherever the screens take
// their users.
type SessionAccounts interface {
	PublicNames(ctx context.Context, reader auth.Subject, ids []string) (map[string]string, error)
	VerifyCredentials(ctx context.Context, tenant, email, password, client string) (account.Account, error)
	Register(ctx context.Context, tenant string, registration account.Registration) (account.Account, error)
	FindForAuthentication(ctx context.Context, tenant, userID string) (account.Account, error)
	Lookup(ctx context.Context, tenant, email string) (account.Account, error)
	MarkVerified(ctx context.Context, tenant, userID, capturedEmail string) (account.Account, bool, error)
	ResetPassword(ctx context.Context, tenant, userID, capturedEmail, capturedFingerprint, newPassword string) (account.Account, error)
	ConfirmPassword(ctx context.Context, subject auth.Subject, password, client string) error
}

// SessionFactors is what the sign-in screens ask of the second factor, the
// method set of *account.Factors they call.
type SessionFactors interface {
	Required(ctx context.Context, tenant, userID string) (bool, error)
	Begin(ctx context.Context, actor auth.Subject, issuer string) (twofactor.Provisioning, error)
	Confirm(ctx context.Context, actor auth.Subject, code string) ([]string, error)
	Disable(ctx context.Context, actor auth.Subject) error
	RegenerateRecoveryCodes(ctx context.Context, actor auth.Subject) ([]string, error)
	VerifyAuthenticator(ctx context.Context, tenant, userID, code string) error
	ConsumeRecovery(ctx context.Context, tenant, userID, code string) error
}

// SessionParts are what SessionMatrix builds the application's sign-in
// screens over: accounts and a second factor it controls, a session store
// whose writes it counts, with the subject resolver installed, the
// application key and the tenant the account belongs to.
type SessionParts struct {
	Users    SessionAccounts
	Factors  SessionFactors
	Sessions *hhttp.SessionStore
	AppKey   []byte
	Tenant   string
}

// SessionPaths are the addresses and the cookie of the sign-in screens. A
// zero field is the published screens' own.
type SessionPaths struct {
	Login         string // "/auth/login"
	Challenge     string // "/auth/two-factor/challenge"
	Recovery      string // "/auth/two-factor/recovery"
	PendingCookie string // "two-factor-pending"
	PendingPath   string // "/auth/two-factor"
}

// SessionFixture is what SessionMatrix is handed.
type SessionFixture struct {
	// Account builds the application's account that signs in, holding the
	// given password hash: the user model, with an id, a tenant, an address
	// and at least one role.
	Account func(passwordHash string) account.Account
	// Subject reads the session subject from an account. Nil uses the
	// account's own Subject method.
	Subject func(account.Account) auth.Subject
	// Handler builds the application's sign-in screens over the parts, the
	// way the application's provider builds them over the real ones.
	Handler func(SessionParts) http.Handler
	// Paths are the screens' addresses; zero fields are the defaults.
	Paths SessionPaths
}

// SessionMatrix drives the application's sign-in screens through every
// outcome of the password and of a pending second factor, and counts the
// sessions they write:
//
//   - a wrong password, a wrong authenticator code, a wrong recovery code, a
//     tampered or expired pending cookie, and a password changed while the
//     second factor was pending write no session;
//   - a right password without a second factor, and a right code or recovery
//     code after a pending password, write exactly one, for the account that
//     signed in, and it loads back through the resolver as that account's
//     subject, roles included;
//   - a password that needs a second factor writes the pending cookie and no
//     session;
//   - a challenge the account's attempt budget has locked ends the pending
//     sign-in: no session, a 303 to the sign-in screen, the pending cookie
//     cleared and Retry-After saying how long the account waits.
//
// The session store has the subject resolver installed over the account as
// it is stored, through the application's own subject: without one the store
// refuses a subject with roles, and no sign-in would write a session.
func SessionMatrix(t *testing.T, f SessionFixture) {
	t.Helper()
	if f.Account == nil || f.Handler == nil {
		t.Fatal("conformance.SessionMatrix needs Account and Handler")
	}
	paths := f.Paths.withDefaults()

	t.Run("AWrongPasswordWritesNoSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		h.users.verifyErr = auth.ErrInvalidCredentials
		h.signIn("wrong-password")
		h.assertWrites(t, 0)
	})
	t.Run("ARightPasswordWithoutAFactorWritesOneSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		h.assertSignedIn(t, h.signIn("right-password"))
	})
	t.Run("ARightPasswordWithAFactorWritesOnlyThePendingCookie", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		h.factors.required = true
		h.pendingCookie(t, h.signIn("right-password"))
		h.assertWrites(t, 0)
	})
	t.Run("AWrongAuthenticatorCodeWritesNoSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		pending := h.startPending(t)
		h.factors.verifyErr = twofactor.ErrInvalidCode
		h.post(paths.Challenge, url.Values{"authenticator_code": {"123456"}}, pending)
		h.assertWrites(t, 0)
	})
	t.Run("ARightAuthenticatorCodeWritesOneSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		pending := h.startPending(t)
		h.assertSignedIn(t, h.post(paths.Challenge, url.Values{"authenticator_code": {"123456"}}, pending))
	})
	t.Run("AWrongRecoveryCodeWritesNoSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		pending := h.startPending(t)
		h.factors.recoveryErr = twofactor.ErrInvalidCode
		h.post(paths.Recovery, url.Values{"recovery_code": {"ABCD-EFGH"}}, pending)
		h.assertWrites(t, 0)
	})
	t.Run("ARightRecoveryCodeWritesOneSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		pending := h.startPending(t)
		h.assertSignedIn(t, h.post(paths.Recovery, url.Values{"recovery_code": {"ABCD-EFGH"}}, pending))
	})
	t.Run("ATamperedPendingCookieWritesNoSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		pending := h.startPending(t)
		pending.Value += "tampered"
		h.post(paths.Challenge, url.Values{"authenticator_code": {"123456"}}, pending)
		h.assertWrites(t, 0)
	})
	t.Run("AnExpiredPendingCookieWritesNoSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		payload, err := json.Marshal(map[string]any{
			"tenant": h.tenant, "user_id": h.users.current.AccountID(),
			"password_fingerprint": account.Fingerprint(h.users.credential.PasswordHash()),
			"remember":             false,
		})
		if err != nil {
			t.Fatalf("encoding the pending state: %v", err)
		}
		expired := &http.Cookie{
			Name:  paths.PendingCookie,
			Value: encryption.NewSigner(h.appKey).Sign(paths.PendingCookie, string(payload), -time.Second),
			Path:  paths.PendingPath,
		}
		h.post(paths.Challenge, url.Values{"authenticator_code": {"123456"}}, expired)
		h.assertWrites(t, 0)
	})
	t.Run("APasswordChangedWhilePendingWritesNoSession", func(t *testing.T) {
		h := newSessionHarness(t, f, paths)
		pending := h.startPending(t)
		h.users.setCurrent(f.Account("replacement-password-hash"))
		h.post(paths.Challenge, url.Values{"authenticator_code": {"123456"}}, pending)
		h.assertWrites(t, 0)
	})
	for _, challenge := range []struct {
		name, path, field string
		fail              func(*sessionFactors, error)
	}{
		{"ALockedAuthenticatorChallengeEndsThePendingSignIn", paths.Challenge, "authenticator_code", func(f *sessionFactors, err error) { f.verifyErr = err }},
		{"ALockedRecoveryChallengeEndsThePendingSignIn", paths.Recovery, "recovery_code", func(f *sessionFactors, err error) { f.recoveryErr = err }},
	} {
		t.Run(challenge.name, func(t *testing.T) {
			h := newSessionHarness(t, f, paths)
			pending := h.startPending(t)
			challenge.fail(h.factors, account.TwoFactorLockedError{RetryAfter: account.ChallengeWindow})
			response := h.post(challenge.path, url.Values{challenge.field: {"123456"}}, pending)
			h.assertWrites(t, 0)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != paths.Login {
				t.Fatalf("the locked challenge answered %d to %q, want 303 to %s", response.Code, response.Header().Get("Location"), paths.Login)
			}
			if got, want := response.Header().Get("Retry-After"), strconv.Itoa(int(account.ChallengeWindow/time.Second)); got != want {
				t.Errorf("Retry-After = %q, want %q", got, want)
			}
			cleared := false
			for _, cookie := range response.Result().Cookies() {
				if cookie.Name == paths.PendingCookie && cookie.MaxAge < 0 {
					cleared = true
				}
			}
			if !cleared {
				t.Error("the locked challenge left the pending sign-in in place")
			}
		})
	}
}

func (p SessionPaths) withDefaults() SessionPaths {
	if p.Login == "" {
		p.Login = "/auth/login"
	}
	if p.Challenge == "" {
		p.Challenge = "/auth/two-factor/challenge"
	}
	if p.Recovery == "" {
		p.Recovery = "/auth/two-factor/recovery"
	}
	if p.PendingCookie == "" {
		p.PendingCookie = "two-factor-pending"
	}
	if p.PendingPath == "" {
		p.PendingPath = "/auth/two-factor"
	}
	return p
}

// sessionAppKey is the application key the harness signs with.
var sessionAppKey = []byte("0123456789abcdef0123456789abcdef")

type sessionHarness struct {
	appKey   []byte
	tenant   string
	email    string
	paths    SessionPaths
	fixture  SessionFixture
	users    *sessionAccounts
	factors  *sessionFactors
	backend  *countingBackend
	sessions *hhttp.SessionStore
	handler  http.Handler
}

func newSessionHarness(t *testing.T, f SessionFixture, paths SessionPaths) *sessionHarness {
	t.Helper()
	// Two values: the account as the password check read it, and as it is
	// stored now, which a subtest changes behind a pending sign-in.
	credential := f.Account("stored-password-hash")
	users := &sessionAccounts{credential: credential, current: f.Account("stored-password-hash")}
	factors := &sessionFactors{}
	backend := &countingBackend{stored: map[string]auth.Subject{}}
	sessions := hhttp.NewSessionStore(sessionAppKey, time.Hour, false, backend)
	sessions.ResolveSubjects(func(_ context.Context, who auth.Subject) (auth.Subject, error) {
		current := users.stored()
		if who.ID != current.AccountID() || who.Tenant != current.AccountTenant() {
			return auth.Subject{}, hhttp.ErrSubjectGone
		}
		return sessionSubject(t, f, current), nil
	})
	handler := f.Handler(SessionParts{
		Users: users, Factors: factors, Sessions: sessions,
		AppKey: sessionAppKey, Tenant: credential.AccountTenant(),
	})
	if handler == nil {
		t.Fatal("SessionFixture.Handler built no handler")
	}
	return &sessionHarness{
		appKey: sessionAppKey, tenant: credential.AccountTenant(), email: credential.AccountEmail(),
		paths: paths, fixture: f, users: users, factors: factors, backend: backend, sessions: sessions, handler: handler,
	}
}

func sessionSubject(t *testing.T, f SessionFixture, a account.Account) auth.Subject {
	s, err := subjectFrom(f.Subject, a)
	if err != nil {
		t.Error(err)
	}
	return s
}

func (h *sessionHarness) signIn(password string) *httptest.ResponseRecorder {
	return h.post(h.paths.Login, url.Values{"email": {h.email}, "password": {password}})
}

func (h *sessionHarness) startPending(t *testing.T) *http.Cookie {
	t.Helper()
	h.factors.required = true
	response := h.signIn("right-password")
	h.assertWrites(t, 0)
	return h.pendingCookie(t, response)
}

func (h *sessionHarness) post(path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The page the form was on, as a browser sends it: where a refused form
	// is sent back to.
	request.Header.Set("Referer", path)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func (h *sessionHarness) pendingCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		// A cookie set with Max-Age or with Expires, and not one deleting it.
		if cookie.Name == h.paths.PendingCookie && cookie.Value != "" && cookie.MaxAge >= 0 {
			return cookie
		}
	}
	t.Fatalf("the password-authenticated attempt wrote no %s cookie (answered %d)", h.paths.PendingCookie, response.Code)
	return nil
}

// assertWrites holds the number of sessions written, and a written one to the
// account that signed in.
func (h *sessionHarness) assertWrites(t *testing.T, want int) {
	t.Helper()
	writes, last := h.backend.read()
	if writes != want {
		t.Fatalf("session writes = %d, want %d", writes, want)
	}
	if want > 0 {
		current := h.users.stored()
		if last.ID != current.AccountID() || last.Tenant != current.AccountTenant() {
			t.Fatalf("the session was written for %q in %q, want %q in %q", last.ID, last.Tenant, current.AccountID(), current.AccountTenant())
		}
	}
}

// assertSignedIn holds a sign-in to one session written for the account, and
// to that session reading back, through the resolver, as the account's
// subject as it is stored: its id, its tenant and its roles.
func (h *sessionHarness) assertSignedIn(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	h.assertWrites(t, 1)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	loaded, err := h.sessions.Load(request.Context(), request)
	if err != nil {
		t.Fatalf("the session the sign-in wrote does not load: %v", err)
	}
	want := sessionSubject(t, h.fixture, h.users.stored())
	if loaded.ID != want.ID || loaded.Tenant != want.Tenant || !slices.Equal(loaded.Roles, want.Roles) {
		t.Fatalf("the session loads as %q in %q with roles %v, want the account's subject %q in %q with roles %v",
			loaded.ID, loaded.Tenant, loaded.Roles, want.ID, want.Tenant, want.Roles)
	}
}

// countingBackend keeps the sessions written, counts them and remembers the
// last subject.
type countingBackend struct {
	mu     sync.Mutex
	writes int
	last   auth.Subject
	stored map[string]auth.Subject
}

func (b *countingBackend) read() (int, auth.Subject) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writes, b.last
}

func (b *countingBackend) Get(_ context.Context, id string) (auth.Subject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.stored[id]
	if !ok {
		return auth.Subject{}, session.ErrExpired
	}
	return s, nil
}

func (b *countingBackend) Put(_ context.Context, id string, s auth.Subject, _ time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes++
	b.last = s
	b.stored[id] = s
	return nil
}

func (*countingBackend) Delete(context.Context, string) error { return nil }

func (*countingBackend) DeleteSubject(context.Context, string, string, string) error { return nil }

// sessionAccounts answers the screens with the fixture's account: as the
// password check read it, and as it is stored now.
type sessionAccounts struct {
	mu         sync.Mutex
	credential account.Account
	current    account.Account
	verifyErr  error
}

func (u *sessionAccounts) stored() account.Account {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.current
}

func (u *sessionAccounts) setCurrent(a account.Account) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.current = a
}

func (*sessionAccounts) PublicNames(context.Context, auth.Subject, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (u *sessionAccounts) VerifyCredentials(context.Context, string, string, string, string) (account.Account, error) {
	if u.verifyErr != nil {
		return nil, u.verifyErr
	}
	return u.credential, nil
}

func (u *sessionAccounts) Register(context.Context, string, account.Registration) (account.Account, error) {
	return u.credential, nil
}

func (u *sessionAccounts) FindForAuthentication(context.Context, string, string) (account.Account, error) {
	return u.stored(), nil
}

func (u *sessionAccounts) Lookup(context.Context, string, string) (account.Account, error) {
	return u.stored(), nil
}

func (u *sessionAccounts) MarkVerified(context.Context, string, string, string) (account.Account, bool, error) {
	return u.stored(), true, nil
}

func (u *sessionAccounts) ResetPassword(context.Context, string, string, string, string, string) (account.Account, error) {
	return u.stored(), nil
}

func (*sessionAccounts) ConfirmPassword(context.Context, auth.Subject, string, string) error {
	return nil
}

// errNotInMatrix is what the factor methods the matrix never reaches answer.
var errNotInMatrix = errors.New("conformance: not used by the session matrix")

// sessionFactors answers the screens with the outcome a subtest chose.
type sessionFactors struct {
	required    bool
	verifyErr   error
	recoveryErr error
}

func (f *sessionFactors) Required(context.Context, string, string) (bool, error) {
	return f.required, nil
}

func (*sessionFactors) Begin(context.Context, auth.Subject, string) (twofactor.Provisioning, error) {
	return twofactor.Provisioning{}, errNotInMatrix
}

func (*sessionFactors) Confirm(context.Context, auth.Subject, string) ([]string, error) {
	return nil, errNotInMatrix
}

func (*sessionFactors) Disable(context.Context, auth.Subject) error { return errNotInMatrix }

func (*sessionFactors) RegenerateRecoveryCodes(context.Context, auth.Subject) ([]string, error) {
	return nil, errNotInMatrix
}

func (f *sessionFactors) VerifyAuthenticator(context.Context, string, string, string) error {
	return f.verifyErr
}

func (f *sessionFactors) ConsumeRecovery(context.Context, string, string, string) error {
	return f.recoveryErr
}

// The two values the screens are handed answer what the account package's
// own types answer, so a method renamed there is a build error here.
var (
	_ SessionAccounts = (*account.Users)(nil)
	_ SessionFactors  = (*account.Factors)(nil)
	_ SessionAccounts = (*sessionAccounts)(nil)
	_ SessionFactors  = (*sessionFactors)(nil)
)
