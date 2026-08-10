package security

import (
	"net/http"
	"time"
)

// IntendedCookieName carries the address somebody was going to, between the
// request a guard turned away and the sign-in that follows it.
//
// Fixed, for the reason SessionCookieName is fixed: the guard writes it and the
// sign-in handler reads it, and two parts of a project that disagree about the
// name is a person who signs in and lands on the front page with no explanation
// -- which is the thing this exists to remove.
//
// It is a cookie and not a row: there is no session yet at the moment the guard
// fires, which is exactly why it fires, so there is nowhere on the server to put
// it that is keyed to this browser.
const IntendedCookieName = "arandu_intended"

// IntendedLifetime is how long the address is worth keeping.
//
// Long enough to find a password in a manager and type it, and short enough that
// it does not survive to an unrelated sign-in: on a shared machine, an address
// kept for a day sends the next person who signs in to the page the previous one
// was refused.
const IntendedLifetime = 10 * time.Minute

// intendedPurpose binds the signature, so a token minted for a verification
// link cannot be pasted in here and turned into a redirect. See Signer.
const intendedPurpose = "session.intended"

// maxIntendedAddress is the longest address worth storing.
//
// A browser drops a cookie over about four kilobytes, silently, and the signed
// form is a third longer than the address plus the signature. Past this the
// cookie would be discarded by the browser rather than by us, which is the same
// outcome arrived at without a decision.
const maxIntendedAddress = 1024

// RememberIntended records where this request was going, so that the sign-in
// screen it is about to be sent to can finish the journey.
//
// It is Laravel's redirect()->guest(): the guard is the only thing that knows
// what the person was reaching for, and by the time they have typed a password
// that request is gone. Without it every sign-in ends at the front page, and
// somebody who followed a link to one invoice has to find it again.
//
// # Why it is signed rather than merely same-site
//
// The value decides where a browser goes immediately after authenticating, so
// whoever can write it can choose where every person in the application lands.
// SameSite=Strict stops another SITE from setting it, and does not stop another
// HOST on the same registrable domain: a cookie set on ".example.com" by
// anything holding a subdomain -- a customer's CNAME, a forgotten staging box, a
// vendor's status page -- arrives here indistinguishable from ours. An HMAC does
// stop it, because the attacker does not have the application key, and it comes
// with the expiry signed into the same bytes so a stale address cannot be
// replayed by keeping the cookie alive.
//
// The destination is checked for being local anyway, on the way in and on the
// way out, because a signature only proves that WE wrote the value.
//
// # What it declines to remember
//
// Only a GET, because the address is replayed by a browser navigation: a POST
// remembered here is a form submission turned into a link, which either answers
// 405 or, on a route that also accepts GET, performs something the person did
// not ask for a second time.
//
// Only a whole page, never an HTMX fragment. A hx-get that is refused would
// otherwise be remembered as "/inbox/rows?page=2", and after signing in the
// person lands on that partial: no layout, no navigation, and it reads as a
// broken deploy. A boosted navigation is a page and is kept -- hx-boost is how
// most links in this stack are followed, so dropping it would drop nearly
// everything.
//
// It writes nothing when there is nothing worth writing, so a caller has no
// branch to get wrong.
func (s *SessionStore) RememberIntended(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		return
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" {
		return
	}
	to, ok := localAddress(r.URL.RequestURI())
	if !ok {
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     IntendedCookieName,
		Value:    s.signer.Sign(intendedPurpose, to, IntendedLifetime),
		Path:     "/",
		MaxAge:   int(IntendedLifetime.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		// Lax and not Strict: the whole point is to be readable on the request
		// that follows a sign-in, and every one of those is same-site anyway.
		// The signature, not the attribute, is what makes the value trustworthy.
		SameSite: http.SameSiteLaxMode,
	})
}

// TakeIntended returns the address RememberIntended stored, and clears it.
//
// It is Laravel's redirect()->intended($fallback), and it is meant to be the
// whole of a sign-in handler's last line:
//
//	httpx.Redirect(w, r, sessions.TakeIntended(w, r, "/"))
//
// The fallback is a parameter rather than a constant because it is the one part
// that genuinely differs -- a blog sends people to the front page, an
// application to its dashboard -- and taking it here means no caller has to
// write the branch for "there was nowhere in particular".
//
// # It answers the fallback for anything it cannot prove
//
// A forged or foreign signature, an expired one, a value that is not a local
// address: all of them are somebody's redirect that is not ours, and none of
// them is worth telling the person about at the moment they have just signed in.
// The check that the address is local is what keeps this from being an open
// redirect -- "sign in and then continue to https://evil.example/login" is the
// oldest phishing link there is, and it belongs here rather than in each
// project's handler precisely because every project would otherwise have to
// remember it.
//
// It is consumed, not read: the cookie is cleared whatever it said, so the
// address is used once. An address left standing is one a person meets again at
// the next sign-in from this browser, weeks later, with no idea why.
func (s *SessionStore) TakeIntended(w http.ResponseWriter, r *http.Request, fallback string) string {
	c, err := r.Cookie(IntendedCookieName)
	if err != nil {
		// No cookie, so nothing to clear: writing the clearing header anyway
		// would put a Set-Cookie on every sign-in in the application, including
		// the ones nobody was ever turned away from.
		return fallback
	}

	s.clearIntended(w)

	payload, err := s.signer.Verify(intendedPurpose, c.Value)
	if err != nil {
		return fallback
	}
	// Checked again, having already been checked before it was stored. The
	// signature proves this application wrote it, and says nothing about what
	// this application wrote a year and three refactors ago -- and this is the
	// side that turns a string into a Location header.
	to, ok := localAddress(payload)
	if !ok {
		return fallback
	}
	return to
}

// clearIntended tells the browser to drop the address.
//
// One function and not two copies of a cookie literal, because the two copies
// have to agree on Path: a clearing cookie written for a different path does not
// replace the one that is there, and the address quietly survives the thing that
// was supposed to spend it.
//
// It is called from TakeIntended, which spends the address, and from
// SessionStore.Destroy, which is signing out -- see there for why.
func (s *SessionStore) clearIntended(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     IntendedCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// localAddress reports whether an address stays inside this application, and is
// the whole of the open-redirect defence.
//
// A destination is accepted only as an absolute path on this origin. That is
// narrow on purpose: "is this URL one of ours" answered by comparing hosts is a
// question with a decade of published bypasses in it, and the only answer that
// has none is not to accept a host at all.
//
// The four shapes it refuses, and what each one does:
//
//   - anything not starting with "/": "https://evil.example/login" and
//     "javascript:..." are both Location headers a browser will act on.
//   - "//evil.example/x": a protocol-relative URL. It arrives here looking like
//     a path -- net/http parses a request target beginning with "//" into
//     URL.Path, host and all -- and a browser resolves it to another origin.
//   - "/\evil.example/x": browsers normalise a backslash to a slash in the
//     authority position, so this is the line above with one character changed,
//     and it is the form that gets past a check written as "starts with a single
//     slash".
//   - a control character or a space anywhere in it: header injection, and the
//     browsers that strip such characters before resolving, which turns a
//     rejected address into an accepted one.
func localAddress(raw string) (string, bool) {
	if len(raw) == 0 || len(raw) > maxIntendedAddress {
		return "", false
	}
	if raw[0] != '/' {
		return "", false
	}
	if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] == 0x7f {
			return "", false
		}
	}
	return raw, true
}
