package middleware

import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
)

// SignInPath is where a guard sends somebody who has to sign in.
//
// Fixed, for the reason security.SessionCookieName is fixed: it is the address
// the framework's auth module registers and the address the starter kit answers
// at, and a configurable one buys nothing while giving two parts of a project a
// way to disagree about where the sign-in screen is.
//
// The address somebody is sent to when they are ALREADY signed in is a
// parameter, because that one genuinely differs -- a blog sends them to the
// front page, an application to its dashboard.
const SignInPath = "/auth/login"

// PasswordConfirmPath is where RequireConfirmedPassword sends somebody who has a
// session but has not typed their password recently.
//
// Fixed for the same reason SignInPath is: it is the address the starter kit
// registers for that screen, and two parts of a project that disagree about it
// produce a guard which redirects to a 404.
const PasswordConfirmPath = "/auth/password/confirm"

// The route guards, and the line they must not cross.
//
// A guard answers two questions and no others: is there a session, and does the
// subject carry this role. It decides NOTHING about a record. Whether this
// subject may read or write THIS row is the Policy's answer, and the Policy
// still runs -- every handler behind a guard still goes through its service and
// therefore through security.Grant, on the write path and on the read path
// alike.
//
// That line is what keeps security.Grant the one authorization path. A guard
// that starts deciding about records is a second one, and with two of them the
// one that gets forgotten is always the guard: a Policy is written once per
// entity and reached from everywhere, while a guard is mounted per route and
// the route somebody adds next month has none. The examples project says the
// same thing about its own adminOnly, in routes/admin.go, and it is worth
// repeating here because this is the file somebody reaches for when a Policy
// feels like too much work.

// RequireAuth refuses a request that carries no session.
//
// It sends the visitor to SignInPath rather than answering 403, because there
// is nothing they can do with a 403 and there is something they can do with the
// sign-in screen.
//
// It remembers where they were going first. This is the only place that knows:
// by the time a password has been typed, the request that was refused is gone,
// and a sign-in that always ends at the front page makes somebody who followed a
// link to one invoice go and find it again. The address goes in a signed cookie
// rather than in the session, because the session is the thing that does not
// exist yet at the moment the guard fires -- see SessionStore.RememberIntended,
// and SessionStore.TakeIntended for the other end of it.
func RequireAuth(sessions *security.SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := sessions.Load(r.Context(), r); err != nil {
				sendToSignIn(sessions, w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RedirectIfAuthenticated is the guest guard: it keeps somebody who is already
// signed in off the screens that exist to sign them in.
//
// Without it the sign-in and registration screens render for a person who has a
// session, which reads to them as having been signed out -- and the next thing
// they do is sign in again, on top of a session that was never gone.
func RedirectIfAuthenticated(sessions *security.SessionStore, to string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := sessions.Load(r.Context(), r); err == nil {
				sendTo(w, r, to)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole admits a subject carrying any one of the roles.
//
// A visitor with no session is sent to the sign-in screen, exactly as
// RequireAuth sends them: "you are not signed in" and "you are signed in as
// somebody without this role" are different situations and only the second one
// is a refusal. That second one is 403 and not 404 -- the page exists, and
// pretending otherwise sends somebody looking for a typo.
//
// It is still not an authorization decision about anything. It reads the roles
// off the session and stops there; the handler behind it goes through the
// Policy like every other handler.
func RequireRole(sessions *security.SessionStore, roles ...string) func(http.Handler) http.Handler {
	// No role at all is RequireAuth spelled a second way, and two names for one
	// guard is how one of them ends up mounted where the other was meant. It is
	// refused at wiring time, which is boot, rather than at the first request --
	// a route that guards nothing answers 200 to everybody and says nothing.
	if len(roles) == 0 {
		panic("middleware: RequireRole was given no role. A guard that asks for no particular role is RequireAuth, and there is one way to spell each")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, err := sessions.Load(r.Context(), r)
			if err != nil {
				sendToSignIn(sessions, w, r)
				return
			}
			for _, role := range roles {
				if subject.HasRole(role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			// Refuse and not http.Error, so that an HTMX request is answered in
			// a shape that client shows somebody. See fhttp.Refuse.
			fhttp.Refuse(w, r, http.StatusForbidden, "this area is not open to your account")
		})
	}
}

// RequireConfirmedPassword admits a request only when the password was typed
// again on this session less than security.PasswordConfirmationWindow ago.
//
// Mount it on the handful of routes where holding the cookie is not proof
// enough that the person is there: changing the address the account is recovered
// through, revealing an API key, closing the account, moving money. The cost of
// not having it is concrete -- a session cookie lifted from a shared machine is a
// full account takeover with no step where the attacker has to know anything.
//
// # Where the line is against a second authorization path
//
// It asks one question: was the password confirmed recently. It never asks may
// this subject touch this record -- that is the Policy's answer, on every service
// call the handler behind it makes, exactly as it is behind RequireAuth. A guard
// that started deciding about records would be a second authorization path, and
// of the two it is always the guard that gets forgotten: a Policy is written once
// per entity and reached from everywhere, a guard is mounted per route and the
// route somebody adds next month has none.
//
// So this is a freshness check on the session, not permission. "Recently
// confirmed" and "allowed" are different facts, and an application that used
// this in place of a Policy would let a confirmed subject reach every record in
// its tenant.
//
// Somebody with no session at all goes to the sign-in screen and not to the
// confirmation screen: there is no password to confirm yet, and sending them to
// a form that asks for one on top of no session is a loop.
func RequireConfirmedPassword(sessions *security.SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, err := sessions.Load(r.Context(), r)
			if err != nil {
				sendToSignIn(sessions, w, r)
				return
			}
			// A package-level function and not a method on the subject: Subject
			// is now an alias for hesape/auth.Subject, and Go forbids declaring
			// a method on another package's type. See security.PasswordConfirmedWithin.
			if security.PasswordConfirmedWithin(subject, security.PasswordConfirmationWindow) {
				next.ServeHTTP(w, r)
				return
			}
			// Where they were going is kept, so the confirmation screen can
			// finish the journey rather than dropping them on the front page --
			// the same half of RequireAuth that is invisible when it is missing.
			// Not when they are already asking for the confirmation screen: that
			// would remember the form as the destination and loop on itself.
			if r.URL.Path != PasswordConfirmPath {
				sessions.RememberIntended(w, r)
			}
			sendTo(w, r, PasswordConfirmPath)
		})
	}
}

// sendToSignIn turns somebody away and keeps where they were going.
//
// The two halves are here, in one function called from both guards, because
// forgetting the first half is invisible: the redirect still works, the person
// still signs in, and they simply land somewhere else -- which nobody reports as
// a bug and everybody experiences.
func sendToSignIn(sessions *security.SessionStore, w http.ResponseWriter, r *http.Request) {
	// Not when they were already asking for the sign-in screen. A guard mounted
	// broadly enough to cover it would otherwise send somebody back to the form
	// they have just filled in, which reads as the password having been wrong.
	if r.URL.Path != SignInPath {
		sessions.RememberIntended(w, r)
	}
	sendTo(w, r, SignInPath)
}

// sendTo answers the redirect a guard turns somebody away with.
//
// The shape of the redirect is fhttp.Redirect's decision -- HX-Redirect and 204
// for an HTMX request, which would otherwise swap the whole sign-in page into
// the div that asked, and 303 for everything else. This wrapper exists for the
// one thing that is the guard's and not every redirect's, which is the header
// below.
//
// A guard's answer is about who is asking, and it MUST NOT be stored by a cache
// shared between people. 204 is heuristically cacheable, so the HTMX answer is
// the one a proxy will happily keep: keep the guest guard's "you are signed in,
// go to /" and it is served to a guest, who is then bounced off the sign-in
// screen and can never sign in at all. The same reasoning routes/admin.go gives
// for putting no-store on the moderation area, arrived at from the other side.
func sendTo(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Cache-Control", "no-store, private")
	fhttp.Redirect(w, r, to)
}
