package security_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
)

// remembered runs the guard's half of the round trip and hands back a request
// carrying whatever cookie it wrote, which is what the browser would send to the
// sign-in screen next.
func remembered(store *security.SessionStore, refused *http.Request) *http.Request {
	rec := httptest.NewRecorder()
	store.RememberIntended(rec, refused)

	next := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	for _, c := range rec.Result().Cookies() {
		next.AddCookie(c)
	}
	return next
}

// asked builds the request net/http would build from this request target.
//
// Not httptest.NewRequest, which parses the target as a URL and hands back
// something already normalised: the shapes worth testing here are exactly the
// ones that survive the server's own parse, and url.ParseRequestURI is what the
// server calls.
func asked(t *testing.T, target string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	u, err := url.ParseRequestURI(target)
	if err != nil {
		t.Fatalf("a server could not have received %q at all: %v", target, err)
	}
	r.URL = u
	return r
}

func TestSomebodyTurnedAwayComesBackToThePageTheyAskedForAndNotTheFrontPage(t *testing.T) {
	store := newStore(true)

	next := remembered(store, httptest.NewRequest(http.MethodGet, "/invoices/42?tab=lines", nil))
	got := store.TakeIntended(httptest.NewRecorder(), next, "/")

	if got != "/invoices/42?tab=lines" {
		t.Fatalf("after signing in they were sent to %q; they had followed a link to /invoices/42?tab=lines and now have to find it again", got)
	}
}

func TestSomebodyWhoWasNotTurnedAwayFromAnywhereGoesWhereTheApplicationSays(t *testing.T) {
	store := newStore(true)

	got := store.TakeIntended(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/login", nil), "/dashboard")

	if got != "/dashboard" {
		t.Fatalf("a sign-in with no intended address went to %q, and the application asked for /dashboard", got)
	}
}

// The address is spent by the sign-in that uses it. Left standing, it is a
// destination somebody meets again at their next sign-in from this browser,
// weeks later, with nothing to explain it.
func TestAnIntendedAddressIsUsedOnceAndThenForgotten(t *testing.T) {
	store := newStore(true)
	next := remembered(store, httptest.NewRequest(http.MethodGet, "/invoices/42", nil))

	rec := httptest.NewRecorder()
	if got := store.TakeIntended(rec, next, "/"); got != "/invoices/42" {
		t.Fatalf("the first sign-in went to %q, want /invoices/42", got)
	}

	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == security.IntendedCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the browser was never told to drop the cookie, so the address is still there at the next sign-in")
	}

	// And the value itself does not work twice, whatever the browser does with
	// the instruction above.
	replay := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	for _, c := range rec.Result().Cookies() {
		replay.AddCookie(c)
	}
	if got := store.TakeIntended(httptest.NewRecorder(), replay, "/"); got != "/" {
		t.Errorf("the same intended address was handed out a second time: %q", got)
	}
}

// The open redirect, in the shape that reaches a handler looking like a path.
//
// "sign in, and then continue to somewhere that is not us" is the oldest
// phishing link there is, and the person following it has just typed a password.
// net/http parses a request target beginning with "//" into URL.Path, host and
// all, so it arrives here looking exactly like a local address -- and a browser
// resolves it to another origin.
//
// The other shapes are refused by localAddress, which is where they can be
// stated one per line; see the internal test beside it.
func TestAnAddressPointingAtAnotherSiteNeverBecomesARedirect(t *testing.T) {
	store := newStore(true)

	next := remembered(store, asked(t, "//evil.example/takeover"))

	if got := store.TakeIntended(httptest.NewRecorder(), next, "/"); got != "/" {
		t.Fatalf("signing in would have sent the browser to %q, which is not this application", got)
	}
}

// Why the cookie is signed rather than merely same-site.
//
// SameSite keeps another SITE from setting it and does nothing about another
// HOST on the same registrable domain -- a customer's CNAME, a forgotten staging
// box -- which can write a cookie on the parent domain that arrives here
// indistinguishable from ours. Whoever writes it chooses where every person in
// the application lands right after authenticating.
func TestAnIntendedAddressThisApplicationDidNotWriteIsIgnored(t *testing.T) {
	ours := newStore(true)
	theirs := security.NewSessionStore([]byte("ffffffffffffffffffffffffffffffff"), time.Hour, true, security.NewMemoryBackend())

	planted := remembered(theirs, httptest.NewRequest(http.MethodGet, "/admin/impersonate/1", nil))

	if got := ours.TakeIntended(httptest.NewRecorder(), planted, "/"); got != "/" {
		t.Fatalf("a cookie signed with somebody else's key chose the destination: %q", got)
	}
}

// A hx-get is a piece of a page, and sending somebody to it after they sign in
// leaves them looking at a partial: no layout, no navigation. It reads as a
// broken deploy.
//
// A boosted navigation is a whole page and is kept -- hx-boost is how most links
// in this stack are followed, so dropping it would drop nearly everything.
func TestAFragmentIsNotRememberedAsAPageAndABoostedNavigationIs(t *testing.T) {
	store := newStore(true)

	fragment := httptest.NewRequest(http.MethodGet, "/inbox/rows?page=2", nil)
	fragment.Header.Set("HX-Request", "true")
	if got := store.TakeIntended(httptest.NewRecorder(), remembered(store, fragment), "/"); got != "/" {
		t.Errorf("after signing in they would land on the bare fragment %q, with no layout around it", got)
	}

	page := httptest.NewRequest(http.MethodGet, "/inbox?page=2", nil)
	page.Header.Set("HX-Request", "true")
	page.Header.Set("HX-Boosted", "true")
	if got := store.TakeIntended(httptest.NewRecorder(), remembered(store, page), "/"); got != "/inbox?page=2" {
		t.Errorf("a boosted link is an ordinary navigation and was forgotten: %q", got)
	}
}

// The address is replayed by a browser navigation, which is a GET. A POST
// remembered here is a form submission turned into a link.
func TestOnlySomethingABrowserCanNavigateToIsRemembered(t *testing.T) {
	store := newStore(true)

	posted := httptest.NewRequest(http.MethodPost, "/invoices/42/pay", nil)
	if got := store.TakeIntended(httptest.NewRecorder(), remembered(store, posted), "/"); got != "/" {
		t.Fatalf("signing in would have navigated to %q, which was a form submission", got)
	}
}

func TestTheIntendedCookieIsNotReadableByScriptsAndDoesNotTravelInTheClear(t *testing.T) {
	rec := httptest.NewRecorder()
	newStore(true).RememberIntended(rec, httptest.NewRequest(http.MethodGet, "/invoices/42", nil))

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name != security.IntendedCookieName {
			continue
		}
		found = true
		if !c.HttpOnly {
			t.Error("the cookie is readable by script, so anything injected into a page can read where the person was going")
		}
		if !c.Secure {
			t.Error("the cookie travels over plain HTTP on a store configured for HTTPS")
		}
		if c.MaxAge <= 0 || time.Duration(c.MaxAge)*time.Second > security.IntendedLifetime {
			t.Errorf("MaxAge = %d, and the address is worth keeping for %s", c.MaxAge, security.IntendedLifetime)
		}
	}
	if !found {
		t.Fatal("nothing was remembered for a plain GET of a page")
	}
}

// Signing out is the moment a shared machine changes hands, and the intended
// address outlives a session by up to IntendedLifetime.
//
// Left standing, the next person to sign in on that browser is carried to the
// page the previous one was refused -- which tells them who was being worked on,
// and reads to them as the application choosing a page at random.
func TestSigningOutForgetsWhereTheLastPersonWasGoing(t *testing.T) {
	store := newStore(true)

	refused := httptest.NewRecorder()
	store.RememberIntended(refused, httptest.NewRequest(http.MethodGet, "/customers/98213/invoices/44", nil))

	// The sign-out, carrying whatever the guard left in the browser.
	out := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	for _, c := range refused.Result().Cookies() {
		out.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	if err := store.Destroy(context.Background(), rec, store.IDFromRequest(out)); err != nil {
		t.Fatalf("signing out: %v", err)
	}

	// The next person, with the browser as the sign-out left it.
	next := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	jar := map[string]*http.Cookie{}
	for _, c := range refused.Result().Cookies() {
		jar[c.Name] = c
	}
	for _, c := range rec.Result().Cookies() {
		jar[c.Name] = c
	}
	for _, c := range jar {
		if c.MaxAge < 0 {
			continue // the browser dropped it
		}
		next.AddCookie(c)
	}

	if got := store.TakeIntended(httptest.NewRecorder(), next, "/"); got != "/" {
		t.Fatalf("the next person to sign in on this machine was sent to %q, which is where the last one was going", got)
	}
}
