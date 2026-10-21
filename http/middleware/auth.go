// The session guards, answered by github.com/arandu-io/hesape/http/middleware.

package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/security"
	hmiddleware "github.com/arandu-io/hesape/http/middleware"
)

// SignInPath is where a guard sends somebody who has to sign in. It is the
// pattern the auth module registers the screen under, named "auth.login".
const SignInPath = hmiddleware.SignInPath

// PasswordConfirmPath is where RequireConfirmedPassword sends somebody whose
// password confirmation is older than the window.
const PasswordConfirmPath = hmiddleware.PasswordConfirmPath

// RequireAuth lets a request with a live session through, carrying its subject
// on the context, and sends anybody else to SignInPath, remembering where they
// were going.
func RequireAuth(sessions *security.SessionStore) func(http.Handler) http.Handler {
	return hmiddleware.RequireAuth(sessions)
}

// LoadSubject puts the subject of a live session on the request context and
// lets every request through, signed in or not.
func LoadSubject(sessions *security.SessionStore) func(http.Handler) http.Handler {
	return hmiddleware.LoadSubject(sessions)
}

// RedirectIfAuthenticated sends somebody already signed in to to, for the
// screens only a visitor needs: sign in, register.
func RedirectIfAuthenticated(sessions *security.SessionStore, to string) func(http.Handler) http.Handler {
	return hmiddleware.RedirectIfAuthenticated(sessions, to)
}

// RequireRole is RequireAuth that also refuses, with 403, a subject holding
// none of roles. It answers "may this session open the area", never whether
// it may touch a record: that is a Policy's.
func RequireRole(sessions *security.SessionStore, roles ...string) func(http.Handler) http.Handler {
	return hmiddleware.RequireRole(sessions, roles...)
}

// RequireConfirmedPassword is RequireAuth that also sends a session whose
// password was not typed again within the confirmation window to
// PasswordConfirmPath.
func RequireConfirmedPassword(sessions *security.SessionStore) func(http.Handler) http.Handler {
	return hmiddleware.RequireConfirmedPassword(sessions)
}
