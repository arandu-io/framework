package feature

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
)

// issuedTokens is a TokenResolver over a table of digests, the way an
// application's token table would hold them. It records every digest it was
// asked about, so a test can say what the guard handed it.
type issuedTokens struct {
	mu       sync.Mutex
	subjects map[middleware.TokenDigest]security.Subject
	asked    []middleware.TokenDigest
	failWith error
}

func newIssuedTokens() *issuedTokens {
	return &issuedTokens{subjects: map[middleware.TokenDigest]security.Subject{}}
}

// issue stores the digest of token, never the token, as the issuing side does.
func (t *issuedTokens) issue(token string, s security.Subject) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subjects[middleware.DigestToken(token)] = s
}

func (t *issuedTokens) ResolveToken(_ context.Context, d middleware.TokenDigest) (security.Subject, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asked = append(t.asked, d)
	if t.failWith != nil {
		return security.Subject{}, t.failWith
	}
	s, ok := t.subjects[d]
	if !ok {
		return security.Subject{}, middleware.ErrUnknownToken
	}
	return s, nil
}

// bearer returns a GET to the dashboard carrying the Authorization header
// exactly as given.
func bearer(authorization string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	return r
}

func TestAHandlerBehindRequireTokenSeesTheTokensSubject(t *testing.T) {
	tokens := newIssuedTokens()
	want := security.Subject{ID: "u-1", Tenant: "acme", Actions: []auth.Action{"invoice.view"}}
	tokens.issue("tok-acme-1", want)
	h, got := behind(middleware.RequireToken(tokens))

	r := bearer("Bearer tok-acme-1")
	// A tenant named by the request is the one thing a guard must never read.
	r.Header.Set("X-Tenant", "globex")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !got.reached {
		t.Fatal("a known token did not reach the action behind RequireToken")
	}
	if !got.ok {
		t.Fatal("ctx.User reported no subject behind RequireToken")
	}
	if got.subject.ID != want.ID || got.subject.Tenant != want.Tenant || !got.subject.Can("invoice.view") {
		t.Errorf("ctx.User = %+v, want the token's subject %+v", got.subject, want)
	}
}

func TestTheSchemeIsReadWhateverItsCase(t *testing.T) {
	tokens := newIssuedTokens()
	tokens.issue("tok-1", security.Subject{ID: "u-1", Tenant: "acme"})
	h, got := behind(middleware.RequireToken(tokens))

	h.ServeHTTP(httptest.NewRecorder(), bearer("bEaReR tok-1"))
	if !got.reached {
		t.Fatal("a known token behind a lowercase scheme was refused; RFC 9110 makes the scheme case-insensitive")
	}
}

// The resolver gets the digest, so an application has no raw token to compare
// in variable time or to write into a log.
func TestTheResolverIsHandedTheDigestAndNeverTheToken(t *testing.T) {
	tokens := newIssuedTokens()
	h, _ := behind(middleware.RequireToken(tokens))

	const token = "s3cret-token-value"
	h.ServeHTTP(httptest.NewRecorder(), bearer("Bearer "+token))

	sum := sha256.Sum256([]byte(token))
	if len(tokens.asked) != 1 {
		t.Fatalf("the resolver was asked %d times, want once", len(tokens.asked))
	}
	if tokens.asked[0] != middleware.TokenDigest(sum) {
		t.Errorf("the resolver was handed %x, want the SHA-256 of the token %x", tokens.asked[0], sum)
	}
	if got, want := middleware.DigestToken(token).String(), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("DigestToken(...).String() = %q, want %q", got, want)
	}
}

// Every way of not being somebody is the same answer, so the answer says
// nothing about which way it was.
func TestEveryRefusedTokenIsAnsweredAlike(t *testing.T) {
	tokens := newIssuedTokens()
	tokens.issue("tok-nobody", security.Subject{Tenant: "acme"})
	tokens.issue("tok-guest", security.Guest("acme"))

	cases := map[string]string{
		"no header":          "",
		"unknown token":      "Bearer tok-never-issued",
		"another scheme":     "Basic dXNlcjpwYXNz",
		"empty bearer":       "Bearer ",
		"subject without id": "Bearer tok-nobody",
		"guest subject":      "Bearer tok-guest",
	}

	for _, accept := range []string{"", "application/json"} {
		var first *httptest.ResponseRecorder
		var firstName string
		for name, authorization := range cases {
			h, got := behind(middleware.RequireToken(tokens))
			r := bearer(authorization)
			if accept != "" {
				r.Header.Set("Accept", accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if got.reached {
				t.Fatalf("%s (Accept %q) reached the action", name, accept)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s (Accept %q): status %d, want 401", name, accept, rec.Code)
			}
			if h := rec.Header().Get("WWW-Authenticate"); h != "Bearer" {
				t.Errorf("%s (Accept %q): WWW-Authenticate = %q, want %q", name, accept, h, "Bearer")
			}
			if first == nil {
				first, firstName = rec, name
				continue
			}
			if rec.Body.String() != first.Body.String() {
				t.Errorf("Accept %q: %s answered %q and %s answered %q -- the body tells them apart",
					accept, name, rec.Body.String(), firstName, first.Body.String())
			}
			if !sameHeaders(rec.Header(), first.Header()) {
				t.Errorf("Accept %q: %s answered headers %v and %s answered %v -- the headers tell them apart",
					accept, name, rec.Header(), firstName, first.Header())
			}
		}
	}
}

func sameHeaders(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb := b[k]
		if strings.Join(va, "\x00") != strings.Join(vb, "\x00") {
			return false
		}
	}
	return true
}

func TestAJSONClientIsRefusedWithAProblemDocument(t *testing.T) {
	h, _ := behind(middleware.RequireToken(newIssuedTokens()))

	r := bearer("Bearer tok-never-issued")
	r.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var doc struct {
		Status int    `json:"status"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the refusal is not a JSON document: %v\n%s", err, rec.Body.String())
	}
	if doc.Status != http.StatusUnauthorized {
		t.Errorf("the document says status %d, want 401", doc.Status)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q: a refusal must not be kept by a shared cache", cc)
	}
}

// A resolver that cannot answer is not a client without a token. Turning a
// store outage into 401 would tell every client its token was revoked.
func TestAResolverThatCannotAnswerIsNotAnsweredAsAnUnknownToken(t *testing.T) {
	outage := errors.New("token table unreachable")
	tokens := newIssuedTokens()
	tokens.failWith = outage
	h, got := behind(middleware.RequireToken(tokens))

	rec := httptest.NewRecorder()
	recovered := func() (v any) {
		defer func() { v = recover() }()
		h.ServeHTTP(rec, bearer("Bearer tok-1"))
		return nil
	}()

	if got.reached {
		t.Fatal("a request whose token could not be checked reached the action")
	}
	err, ok := recovered.(error)
	if !ok || !errors.Is(err, outage) {
		t.Fatalf("recovered %v, want the resolver's error on the panic path; the answer was %d", recovered, rec.Code)
	}
}

// A cookie riding along with the token must not widen what the token may do.
func TestASubjectAlreadyOnTheRequestDoesNotWidenTheToken(t *testing.T) {
	tokens := newIssuedTokens()
	narrow := security.Subject{ID: "u-1", Tenant: "acme", Actions: []auth.Action{"invoice.view"}}
	tokens.issue("tok-read-only", narrow)
	h, got := behind(middleware.RequireToken(tokens))

	wide := security.Subject{ID: "u-1", Tenant: "acme", Actions: []auth.Action{"invoice.view", "invoice.delete"}}
	r := bearer("Bearer tok-read-only")
	r = r.WithContext(auth.WithSubject(r.Context(), wide))
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !got.reached || !got.ok {
		t.Fatal("the token's request did not reach the action with a subject")
	}
	if got.subject.Can("invoice.delete") {
		t.Errorf("ctx.User = %+v: the session's subject was kept and the read-only token can delete", got.subject)
	}
}

func TestRequireTokenRefusesANilResolverAtWiring(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("RequireToken(nil) wired a guard that has nothing to check a token against")
		}
	}()
	middleware.RequireToken(nil)
}
