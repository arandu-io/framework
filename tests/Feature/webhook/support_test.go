package feature

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	testbase "github.com/arandu-io/framework/tests"
	fwebhook "github.com/arandu-io/framework/webhook"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// These tests drive the native webhook module through the Framework router the
// way an application does: build it with the native New, hand it to
// fwebhook.NewModule, register its routes, make a request.
//
// The database handle wraps nothing, and that is the assertion. A request that
// reached a statement would panic, so every answer below is proof that the
// refusal happened in the guard, the policy or the receiver, and not after a
// read.

// appKey is the key the session store, the flash and the module are built
// over. Any thirty-two bytes will do here.
const appKey = "0123456789abcdef0123456789abcdef"

// reservedPrefix is the namespace the framework keeps for itself, refused at
// boot to every module but its own.
const reservedPrefix = "/_arandu"

// noDatabase is a handle that wraps nothing: reaching a statement through it
// panics.
func noDatabase() *data.DB { return data.Wrap(nil, data.DialectSQLite) }

// newSessions is a session store over memory, resolving the subjects signIn
// enrolls.
func newSessions() *security.SessionStore {
	return testbase.NewSessions([]byte(appKey), time.Hour)
}

// mounted is the module registered on a Framework router the way the kernel
// builds one, with the session store its guard reads and the flash its router
// answers a rejected form with.
type mounted struct {
	native   *hwebhook.Module
	router   *fhttp.Router
	sessions *security.SessionStore
	flash    *security.Flash
}

// mount builds the native module behind RequireAuth, wraps it, and registers
// it on a router carrying a flash.
func mount(t *testing.T, cfg hwebhook.Config) mounted {
	t.Helper()

	sessions := newSessions()
	flash := security.NewFlash([]byte(appKey), false)

	cfg.Key = []byte(appKey)
	native, err := hwebhook.New(cfg, noDatabase(), middleware.RequireAuth(sessions))
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	module := fwebhook.NewModule(native)

	// WithFlash is what the kernel does at boot. Without it the router has
	// nowhere to put a rejected input and answers it as a failure.
	router := fhttp.NewRouter().WithFlash(flash)
	module.Routes(router.ForModule(module.Name()))
	return mounted{native: native, router: router, sessions: sessions, flash: flash}
}

// administrator is the most privileged subject an application can produce
// without granting the package's actions.
func administrator() security.Subject {
	return security.Subject{ID: "user-1", Tenant: "acme", Roles: []string{"admin"}, Verified: true}
}

// signIn starts a session for the subject and returns the cookies a browser
// would send back with the next request.
func (m mounted) signIn(t *testing.T, subject security.Subject) []*http.Cookie {
	t.Helper()

	testbase.Enroll(m.sessions, subject)
	rec := httptest.NewRecorder()
	if _, err := m.sessions.Start(context.Background(), rec, subject); err != nil {
		t.Fatalf("starting a session: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("starting a session wrote no cookie")
	}
	return cookies
}

// answer makes one form request against the router and returns the recorder.
func (m mounted) answer(t *testing.T, method, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return serve(t, m.router, req)
}

// serve answers one request with handler. A panic is a failure of the test
// rather than of the run: it is how an error nobody claimed, or a statement
// on the empty handle, arrives here.
func serve(t *testing.T, handler http.Handler, req *http.Request) (rec *httptest.ResponseRecorder) {
	t.Helper()

	rec = httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("%s %s panicked, which a deployed router answers with 500: %v", req.Method, req.URL.Path, recovered)
		}
	}()
	handler.ServeHTTP(rec, req)
	return rec
}

// publishedRoute is one route as the separate webhook module registered it:
// the method, the pattern under the default prefixes, and the name.
type publishedRoute struct{ method, pattern, name string }

// publishedManagementRoutes are the management routes the separate webhook
// module registered, under its default prefix, with the names URLs were built
// from. A name is a promise to every caller that built a URL from it, so the
// bridge has to register exactly these.
var publishedManagementRoutes = []publishedRoute{
	{http.MethodGet, "/webhook/endpoints", "webhook.endpoints.index"},
	{http.MethodPost, "/webhook/endpoints", "webhook.endpoints.store"},
	{http.MethodGet, "/webhook/endpoints/{id}", "webhook.endpoints.show"},
	{http.MethodPut, "/webhook/endpoints/{id}", "webhook.endpoints.update"},
	{http.MethodDelete, "/webhook/endpoints/{id}", "webhook.endpoints.destroy"},
	{http.MethodGet, "/webhook/endpoints/{id}/secret", "webhook.endpoints.secret"},
	{http.MethodPost, "/webhook/endpoints/{id}/test", "webhook.endpoints.test"},
	{http.MethodGet, "/webhook/endpoints/{id}/deliveries", "webhook.endpoints.deliveries"},
	{http.MethodPost, "/webhook/endpoints/{id}/rotate", "webhook.endpoints.rotate"},
	{http.MethodPost, "/webhook/endpoints/{id}/enable", "webhook.endpoints.enable"},
	{http.MethodPost, "/webhook/endpoints/{id}/disable", "webhook.endpoints.disable"},
	{http.MethodGet, "/webhook/deliveries/{id}", "webhook.deliveries.show"},
	{http.MethodGet, "/webhook/deliveries/{id}/attempts", "webhook.deliveries.attempts"},
	{http.MethodPost, "/webhook/deliveries/{id}/redeliver", "webhook.deliveries.redeliver"},
	{http.MethodGet, "/webhook/calls", "webhook.calls.index"},
	{http.MethodGet, "/webhook/calls/{id}", "webhook.calls.show"},
	{http.MethodPost, "/webhook/calls/{id}/process", "webhook.calls.process"},
}

// accept is a processor that takes every call.
func accept(context.Context, security.Grant, *hwebhook.Call) error { return nil }

// billing is a static receiver answering POST and PUT, whose routes the
// separate module named webhook.receive.billing and
// webhook.receive.billing.put.
func billing() hwebhook.Receiver {
	return hwebhook.Receiver{
		Name: "billing", Tenant: "acme", Secret: appKey, Methods: []string{http.MethodPost, http.MethodPut},
		Process: hwebhook.ProcessorFunc(accept),
	}
}

// tokenKey signs the tokens in the path, as an application's signer would.
const tokenKey = "token-key-token-key-token-key-012"

// token is the path segment that names a record: its tenant and id, and a
// signature over both that only the application can make.
func token(tenantID, id string) string {
	mac := hmac.New(sha256.New, []byte(tokenKey))
	mac.Write([]byte(tenantID + "." + id))
	return tenantID + "." + id + "." + hex.EncodeToString(mac.Sum(nil))
}

// cieloSecret is the environment's notification secret, sent as it is.
const cieloSecret = "cielo-notification-secret-0123456"

// cieloResolver reads the token, checks its signature, and resolves the one
// record these tests know. A token whose tenant is "down" stands for a
// directory that is not answering.
var cieloResolver = hwebhook.ResolverFunc(func(r *http.Request) (hwebhook.Resolution, error) {
	parts := strings.Split(r.PathValue("token"), ".")
	if len(parts) != 3 {
		return hwebhook.Resolution{}, errors.New("not a token")
	}
	if parts[0] == "down" {
		return hwebhook.Resolution{}, fmt.Errorf("the inbox: %w", hwebhook.ErrReceiverUnavailable)
	}
	if !hmac.Equal([]byte(token(parts[0], parts[1])), []byte(r.PathValue("token"))) {
		return hwebhook.Resolution{}, errors.New("the token was not signed here")
	}
	if parts[0] != "acme" || parts[1] != "ep-1" {
		return hwebhook.Resolution{}, errors.New("no such inbound endpoint")
	}
	return hwebhook.Resolution{Tenant: "acme", Secret: cieloSecret, Reference: "ep-1"}, nil
})

// filing is a synchronous processor that records the calls it was given.
// Every request in these tests is refused before it, so a call reaching it is
// a failure.
type filing struct {
	mu    sync.Mutex
	calls int
}

// ProcessNow records the call and answers 200.
func (f *filing) ProcessNow(context.Context, security.Grant, *hwebhook.Call) (hwebhook.ProcessResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return hwebhook.ProcessResult{Status: http.StatusOK}, nil
}

// notification is a call to a receiver carrying the shared secret in its
// header.
func notification(path, body, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Perata-Notification", secret)
	}
	return req
}
