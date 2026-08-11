package security_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
)

func testFlash() *security.Flash {
	return security.NewFlash([]byte(strings.Repeat("k", 32)), false)
}

// pageRead is the only kind of request a one-shot message is worth spending on:
// a browser navigating to a page.
func pageRead(cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/signup", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

// written returns the flash cookie a response set, or nil.
func written(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == security.FlashCookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

// cleared reports that a response told the browser to drop the flash.
func cleared(rec *httptest.ResponseRecorder) bool {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == security.FlashCookieName && c.MaxAge < 0 {
			return true
		}
	}
	return false
}

func TestFlashSurvivesExactlyOneRequest(t *testing.T) {
	f := testFlash()

	rec := httptest.NewRecorder()
	f.Write(rec, map[string][]string{"email": {"is required"}}, url.Values{"name": {"Ada"}})
	cookie := written(rec)
	if cookie == nil {
		t.Fatal("Write set no cookie")
	}

	first := httptest.NewRecorder()
	errs, old, ok := f.Take(first, pageRead(cookie))
	if !ok {
		t.Fatal("the first read got nothing")
	}
	if got := errs["email"]; len(got) != 1 || got[0] != "is required" {
		t.Errorf("errors = %v", errs)
	}
	if got := old.Get("name"); got != "Ada" {
		t.Errorf("old name = %q, want %q", got, "Ada")
	}
	if !cleared(first) {
		t.Error("the read did not tell the browser to drop the cookie, so a reload shows the message again")
	}

	// A browser that honoured the clearing sends nothing on the next request.
	// This asserts the other half: replaying the same value is not what keeps
	// the message alive -- being cleared is.
	second := httptest.NewRecorder()
	if _, _, ok := f.Take(second, pageRead()); ok {
		t.Error("a request carrying no flash cookie produced one anyway")
	}
}

func TestFlashNeverCarriesAPasswordValueButAlwaysItsMessage(t *testing.T) {
	f := testFlash()

	rec := httptest.NewRecorder()
	f.Write(rec,
		map[string][]string{"password": {"must be at least 12 characters"}},
		url.Values{
			"email":                 {"ada@example.test"},
			"password":              {"hunter2"},
			"password_confirmation": {"hunter2"},
			"current_password":      {"letmein"},
			// Not on Laravel's list, and the reason the suffix rule exists: an
			// exact-name list lets this through while looking complete.
			"owner_password": {"s3cret"},
			"_token":         {"a-stale-csrf-token"},
		})

	cookie := written(rec)
	if cookie == nil {
		t.Fatal("Write set no cookie")
	}
	for _, secret := range []string{"hunter2", "letmein", "s3cret", "a-stale-csrf-token"} {
		if strings.Contains(cookie.Value, secret) {
			t.Errorf("%q is in the cookie value", secret)
		}
	}

	errs, old, ok := f.Take(httptest.NewRecorder(), pageRead(cookie))
	if !ok {
		t.Fatal("nothing came back")
	}
	// The message is the whole point: an empty password box that does not say
	// why it was rejected is the failure being fixed, not the fix.
	if got := errs["password"]; len(got) != 1 || got[0] != "must be at least 12 characters" {
		t.Errorf("the password's message did not survive: %v", errs)
	}
	for _, field := range []string{"password", "password_confirmation", "current_password", "owner_password", "_token"} {
		if got := old.Get(field); got != "" {
			t.Errorf("old %s = %q, want empty", field, got)
		}
	}
	if got := old.Get("email"); got != "ada@example.test" {
		t.Errorf("old email = %q: what is not secret must come back", got)
	}
}

func TestFlashIsNotConsumedByAnAssetRequest(t *testing.T) {
	f := testFlash()

	rec := httptest.NewRecorder()
	f.Write(rec, map[string][]string{"email": {"is required"}}, nil)
	cookie := written(rec)

	// The requests a page makes on arrival, none of which draws a message.
	for _, spend := range []struct {
		name  string
		build func() *http.Request
	}{
		{name: "a stylesheet", build: func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
			r.Header.Set("Accept", "text/css,*/*;q=0.1")
			return r
		}},
		{name: "an HTMX fragment", build: func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/inbox/rows", nil)
			r.Header.Set("Accept", "*/*")
			r.Header.Set("HX-Request", "true")
			return r
		}},
		{name: "a JSON call", build: func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			r.Header.Set("Accept", "application/json")
			return r
		}},
		{name: "a POST", build: func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/signup", nil)
			r.Header.Set("Accept", "text/html")
			return r
		}},
	} {
		t.Run(spend.name, func(t *testing.T) {
			req := spend.build()
			req.AddCookie(cookie)

			rec := httptest.NewRecorder()
			if _, _, ok := f.Take(rec, req); ok {
				t.Errorf("%s was handed the messages, which it cannot draw", spend.name)
			}
			if cleared(rec) {
				t.Errorf("%s spent the flash: the page underneath it renders with nothing on it", spend.name)
			}
		})
	}

	// And the page that follows still gets them.
	if _, _, ok := f.Take(httptest.NewRecorder(), pageRead(cookie)); !ok {
		t.Error("the page did not get the messages after the fragments left them alone")
	}
}

func TestAForgedFlashIsIgnored(t *testing.T) {
	f := testFlash()

	honest := httptest.NewRecorder()
	f.Write(honest, map[string][]string{"email": {"is required"}}, nil)
	mine := written(honest)

	// Another application's key. The value is well formed and correctly signed
	// -- by somebody else.
	other := security.NewFlash([]byte(strings.Repeat("x", 32)), false)
	elsewhere := httptest.NewRecorder()
	other.Write(elsewhere, map[string][]string{"email": {"anything at all"}}, nil)
	foreign := written(elsewhere)

	// A token this application DID mint, for something else. Purpose binding is
	// what stops a verification link being pasted in here.
	signer := security.NewSigner([]byte(strings.Repeat("k", 32)))
	misused := signer.Sign("session.intended", "e:email=is+required", security.FlashLifetime)

	for _, forged := range []struct{ name, value string }{
		{"another application's signature", foreign.Value},
		{"a token minted for another purpose", misused},
		{"an edited payload", "e:email=you+are+an+admin." + strings.SplitN(mine.Value, ".", 2)[1]},
		{"not a token at all", "hello"},
	} {
		t.Run(forged.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			_, _, ok := f.Take(rec, pageRead(&http.Cookie{Name: security.FlashCookieName, Value: forged.value}))
			if ok {
				t.Error("accepted")
			}
			if !cleared(rec) {
				t.Error("left in the browser: a value that cannot be trusted is one to be rid of")
			}
		})
	}
}

func TestAnOversizeFlashDropsOldInputBeforeMessages(t *testing.T) {
	f := testFlash()

	// A textarea nobody can carry in a cookie, beside two short messages.
	rec := httptest.NewRecorder()
	f.Write(rec,
		map[string][]string{
			"title": {"is required"},
			"body":  {"must be at most 2000 characters"},
		},
		url.Values{
			"body":  {strings.Repeat("a very long draft. ", 500)},
			"title": {"A title"},
		})

	cookie := written(rec)
	if cookie == nil {
		t.Fatal("Write set no cookie")
	}
	if len(cookie.Value) > security.MaxFlashBytes {
		t.Fatalf("cookie is %d bytes, over the %d budget: a browser drops it silently and the screen says nothing",
			len(cookie.Value), security.MaxFlashBytes)
	}

	errs, old, ok := f.Take(httptest.NewRecorder(), pageRead(cookie))
	if !ok {
		t.Fatal("nothing came back")
	}
	if len(errs) != 2 {
		t.Errorf("messages were given up before the input was: %v", errs)
	}
	if got := old.Get("body"); got != "" {
		t.Error("the long draft was kept, so something else had to go")
	}
	// The short field survives: what is given up is the expensive one, not
	// everything.
	if got := old.Get("title"); got != "A title" {
		t.Errorf("old title = %q, want %q -- the cheap field cost nothing to keep", got, "A title")
	}
}

func TestLocalPathIsTheSameAnswerTheIntendedAddressGets(t *testing.T) {
	// The bypasses this defence exists for, asserted here because Reject hands
	// it an address that comes off a header the visitor writes.
	for _, refused := range []string{
		"https://evil.example/login",
		"//evil.example/x",
		"/\\evil.example/x",
		"javascript:alert(1)",
		"/ok\nLocation: https://evil.example",
		"",
	} {
		if _, ok := security.LocalPath(refused); ok {
			t.Errorf("LocalPath(%q) was accepted", refused)
		}
	}
	if to, ok := security.LocalPath("/posts/new?draft=1"); !ok || to != "/posts/new?draft=1" {
		t.Errorf("LocalPath refused an ordinary address: %q %v", to, ok)
	}
}
