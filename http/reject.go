// The answer to a rejected form, answered by github.com/arandu-io/hesape/http.
//
// It stayed with the request layer and not with the session: what it writes is
// a redirect, and the whole of its correctness is that the address it redirects
// to is proven local.

package http

import (
	"net/http"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	hhttp "github.com/arandu-io/hesape/http"
	hvalidation "github.com/arandu-io/hesape/validation"
)

// Reject answers a request whose input failed the rules: back where it came
// from, with the messages and with what was typed still in the boxes.
//
// The answer to a rejected form is a redirect, not a body. A 422 carrying the
// messages in the markup is thrown away by htmx unless the layout has
// reconfigured its response handling, and even where it was swapped in a reload
// re-posted the form.
//
// The errs parameter keeps the framework's validation.Errors rather than
// naming hesape's, so the signature every caller is written against is
// unchanged. The conversion below is what makes that safe whichever way the
// validation bridge goes: it is identity while framework/validation.Errors is
// an alias for hesape's, and it is still correct if it ever goes back to being
// a defined type over the same map[string][]string.
//
// security.Flash is already an alias for hesape/session.Flash, so the flash a
// caller holds is the value hesape/http.Reject expects.
func Reject(w http.ResponseWriter, r *http.Request, f *security.Flash, errs validation.Errors) {
	hhttp.Reject(w, r, f, hvalidation.Errors(errs))
}

// Back is the address a rejected request is sent to: where it came from, or "/".
//
// It is exported because a handler that answers a rejection itself -- one that
// has a domain reason rather than a rule failure -- needs the same address, and
// a second reading of the Referer header is a second place for the
// open-redirect check to be missing.
//
// It answers "/" for everything it cannot prove is ours: no Referer, an
// unparseable one, one on another host, or a path LocalPath refuses.
func Back(r *http.Request) string { return hhttp.Back(r) }
