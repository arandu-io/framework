package httpx

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// Reject answers a request whose input failed the rules: back where it came
// from, with the messages and with what was typed still in the boxes.
//
// It is Laravel's Handler::invalid, which is three calls --
//
//	redirect(url()->previous())
//	  ->withInput(Arr::except($request->input(), $this->dontFlash))
//	  ->withErrors($exception->errors())
//
// -- and the shape is deliberately the same one: the answer to a rejected form
// is a redirect, not a body. The body was tried first and is the failure that
// produced this file. A 422 carrying the messages in the markup is thrown away
// by htmx unless the layout has reconfigured its response handling, so the
// person saw the form they submitted, unchanged, with nothing on it; and even
// where it was swapped in, a reload re-posted the form. A redirect is read by
// every client there is, and the page that follows it is a page.
//
// # What "back" means, and why it is checked
//
// The Referer, and "/" when there is none or it is not ours. That address ends
// up in a Location header, so it is chosen by whoever sent the request:
// "https://evil.example/login" is a Referer somebody can send, and answering it
// turns every form in the application into an open redirect. security.LocalPath
// is the check, and it is the same one the intended-destination cookie has used
// since it existed -- one definition of "is this address ours", carrying its
// four documented bypasses, rather than a second one written here that knows
// about three of them.
//
// # What goes in the flash
//
// The posted form, minus the fields security.Flash never carries back, and the
// messages. The query string is deliberately not included: it is already in the
// address being returned to, and adding it would put the same values on the page
// twice with a chance of the two disagreeing.
func Reject(w http.ResponseWriter, r *http.Request, f *security.Flash, errs validation.Errors) {
	if f != nil {
		// Parsed here rather than assumed: a handler that rejected on the first
		// rule may never have read the body, and the whole value of what was
		// typed is that it goes back in the boxes. ParseForm is a no-op when the
		// form has already been read.
		_ = r.ParseForm()
		f.Write(w, errs, r.PostForm)
	}

	// A rejected form is one person's typed input coming back to them. It must
	// never be stored by a cache shared between people -- the same reasoning the
	// route guards give for their own answers, arrived at from the other side.
	w.Header().Set("Cache-Control", "no-store, private")

	// Redirect and not http.Redirect: an HTMX request gets HX-Redirect and a
	// plain one gets 303, decided in one place for every redirect the framework
	// answers. See Redirect.
	Redirect(w, r, Back(r))
}

// Back is the address a rejected request is sent to: where it came from, or "/".
//
// It is Laravel's url()->previous(). Exported because a handler that answers a
// rejection itself -- one that has a domain reason rather than a rule failure --
// needs the same address, and a second reading of the Referer header is a second
// place for the open-redirect check to be missing.
//
// It answers "/" for everything it cannot prove is ours: no Referer, an
// unparseable one, one on another host, or a path security.LocalPath refuses.
// Landing on the front page is a bad answer; landing on somebody else's site
// carrying the fact that you just submitted a form is a worse one.
func Back(r *http.Request) string {
	referer := r.Header.Get("Referer")
	if referer == "" {
		return "/"
	}

	u, err := url.Parse(referer)
	if err != nil {
		return "/"
	}
	// A Referer with a host that is not this one is another site, and a Referer
	// with no host at all is already a path. Both are compared against the Host
	// header rather than against a configured origin, because the Host header is
	// what routed this request here -- and the only thing being decided is
	// whether to hand the address back to the same browser that sent it.
	if u.Host != "" && !strings.EqualFold(u.Host, r.Host) {
		return "/"
	}

	to, ok := security.LocalPath(u.RequestURI())
	if !ok {
		return "/"
	}
	return to
}
