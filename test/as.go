package test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arandu-io/hesape/auth"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

// As signs the account in on the server's application and returns a browser
// carrying that session.
//
//	alice := tests.Register(t, server, "alice@example.com")
//	test.As(t, server, auth.Subject{ID: alice.ID, Tenant: alice.Tenant}).
//		Get(server.Route("invoices.index")).AssertOk()
//
// The session is started on the application's own session store, the call
// its sign-in makes once the password is right, and nothing else is done: no
// subject is put on a request, and no guard is passed over. Every request
// afterwards is read by the application's RequireAuth like the request of
// somebody who typed a password, its SubjectResolver answers who the account
// is now, and auth.SubjectFrom reads what they answered. A role taken away in
// the test is gone on the next request, exactly as in production.
//
// who is the account and nothing else: its ID and Tenant. Roles and actions
// are the resolver's to answer, from the database, and a subject carrying
// them is refused rather than having them silently dropped -- a test that
// wrote Roles: admin and got a 403 would be chasing the wrong bug. The account
// must exist where the resolver reads it, so the session As started is read
// back once before As returns, and a resolver that does not know the account
// fails the test here, naming it.
//
// The browser calls the application in process. For a browser driven over the
// socket -- an end-to-end journey -- hand it [Client.Cookies]: the session is
// the application's, so the same cookies sign it in at [Server.URL].
func As(t *testing.T, server *Server, who auth.Subject) *Client {
	t.Helper()
	if server == nil {
		t.Fatalf("As was handed a nil server")
	}
	cookies, err := signIn(t.Context(), server.sessions, who)
	if err != nil {
		t.Fatalf("As(%s of %s): %v", who.ID, who.Tenant, err)
	}
	client := NewClient(t, server.handler)
	for _, ck := range cookies {
		client.keep(ck)
	}
	return client
}

// signIn starts the session for who on sessions and reads it back the way
// RequireAuth reads it, answering the cookies the browser is handed.
func signIn(ctx context.Context, sessions *hhttp.SessionStore, who auth.Subject) ([]*http.Cookie, error) {
	if sessions == nil {
		return nil, errors.New("the application Serve booted has no session store: " +
			"set Application.Sessions to the store its guards read")
	}
	if err := checkIdentity(who); err != nil {
		return nil, err
	}

	rec := httptest.NewRecorder()
	if _, err := sessions.Start(ctx, rec, who); err != nil {
		return nil, fmt.Errorf("starting the session: %w", err)
	}
	cookies := rec.Result().Cookies()

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	if _, err := sessions.Load(ctx, req); err != nil {
		if errors.Is(err, session.ErrExpired) {
			return nil, fmt.Errorf("the session store refused the session it had just started (%w); "+
				"its SubjectResolver does not find this account in this tenant, so create the account "+
				"in the test's database before signing it in", err)
		}
		return nil, fmt.Errorf("reading the session back: %w", err)
	}
	return cookies, nil
}

// checkIdentity refuses anything in who that is not the account's identity.
func checkIdentity(who auth.Subject) error {
	switch {
	case who.IsGuest():
		return errors.New("a guest has no account to sign in; a request with no session is a client " +
			"that never called As")
	case who.ID == "" || who.Tenant == "":
		return errors.New("the subject names no account: As needs its ID and its Tenant")
	case len(who.Roles) > 0 || len(who.Actions) > 0:
		return errors.New("the subject carries roles or actions; they are the SubjectResolver's to answer " +
			"from the account, so give the account the role where the resolver reads it and pass only " +
			"ID and Tenant")
	case who.Impersonator != nil || who.Acting:
		return errors.New("the subject carries an impersonation, which a sign-in never starts")
	}
	return nil
}
