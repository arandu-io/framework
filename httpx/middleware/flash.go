// The flash-consuming middleware. It is NOT a bridge, and the plan said it
// would be: docs/31:187 has it dissolving into a session.Start that carries and
// consumes. hesape/session/middleware.StartSession is Illuminate's session
// bootstrap and does not touch the arandu flash cookie; nothing in hesape calls
// hesape/http.WithState outside a view test. Deleting this file would leave the
// three-step path with its middle step missing, and the symptom -- every
// rejected form coming back blank -- is exactly the failure the path exists to
// remove. It stays until hesape answers for it.

package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/security"
)

// Flash consumes the one-shot flash and puts it on the request, so the page
// about to render can draw the messages of the request that was redirected here.
//
// It is the middle of three steps that no application writes any of: httpx.Reject
// puts the messages in the browser, this takes them out again, and view.New
// copies them onto the Page every screen already embeds. The handler in between
// passes nothing, which is the point -- the failure being fixed is a message that
// existed and did not reach the screen, and every hand-written hop is another
// place for it to stop.
//
// The kernel installs it. It is not in the application's pipeline because there
// is nothing to decide about it: a pipeline entry an application can leave out is
// one an application leaves out, and the symptom is a form that comes back blank
// -- indistinguishable from having no flash at all.
func Flash(f *security.Flash) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Nothing wired, nothing to consume. A nil flash is what a kernel
			// built without an application key would hand over, and a middleware
			// that panicked on it would take down every page rather than the one
			// feature that is missing.
			if f == nil {
				next.ServeHTTP(w, r)
				return
			}

			errs, old, ok := f.Take(w, r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			// This response carries one person's messages and one person's typed
			// input. A cache shared between people must not keep it: the next
			// visitor to the same address would be shown somebody else's form,
			// filled in, with somebody else's errors on it.
			w.Header().Set("Cache-Control", "no-store, private")

			// errs comes back from the flash as a plain map[string][]string,
			// which assigns straight to the field: httpx.State is now an alias
			// for hesape/http.State and its Errors is hesape/validation.Errors,
			// the same underlying map this has always carried.
			state := httpx.State{Errors: errs, Old: old}
			next.ServeHTTP(w, r.WithContext(httpx.WithState(r.Context(), state)))
		})
	}
}
