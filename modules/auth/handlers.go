package auth

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
)

// Handlers are thin on purpose: extract the input, delegate to the service,
// render. No business rule and no repository access lives here -- `aru doctor`
// complains when a handler imports the data package.

// loginPage is what the sign-in screen renders from.
//
// A struct rather than a map, for the reason `aru doctor` refuses a map behind
// ctx.View: a misspelled key in a template renders as nothing, so the screen
// comes up with the error box missing and nobody finds out. Here that would be
// the refusal disappearing.
type loginPage struct {
	CSRFToken string
	// Email is what the person typed, put back in the field. A sign-in screen
	// that clears the address on every wrong password makes somebody type it
	// again to find out they had the password wrong, not the address.
	Email string
	// Errors is what went wrong, empty on the first visit.
	Errors validation.Errors
	// Action is where the form posts. It is read from middleware.SignInPath
	// rather than written into the markup, because a copy of the address here is
	// one that keeps pointing at the old screen after the constant moves -- and
	// a form posting to a 404 loses the password somebody just typed.
	Action string
	// Stylesheet and HTMX are content-addressed, so they are read here rather
	// than written as constants: the URL changes with the bytes, and a
	// hard-coded one would serve last build's stylesheet forever.
	Stylesheet string
	HTMX       string
}

// showLogin renders the login form with a fresh CSRF token.
func (m *Module) showLogin(w http.ResponseWriter, r *http.Request) {
	m.renderLogin(w, r, http.StatusOK, "", nil)
}

// renderLogin draws the sign-in screen, with whatever went wrong on it.
//
// One function for the first visit and for every refusal, because answering a
// refusal with a bare <div class="alert"> and no document around it reaches
// nobody. To a browser that fragment is the whole page -- an error message on a
// blank background, with no form to type into and no way back except the back
// button -- and to htmx it is nothing at all, because htmx swaps no 4xx and the
// sentence goes into a body it discards. A wrong password is the most common
// refusal this framework answers.
//
// The status is the caller's and is unchanged: 401 for wrong credentials, 422
// for a form that did not validate, 429 for the lockout, all of them with the
// screen the person can act on attached.
func (m *Module) renderLogin(w http.ResponseWriter, r *http.Request, status int, email string, errs validation.Errors) {
	token, err := m.svc.csrf.Issue(m.svc.session.IDFromRequest(r))
	if err != nil {
		observability.Log(r.Context()).Error("issuing csrf token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginForm.Execute(w, loginPage{
		CSRFToken:  token,
		Email:      email,
		Errors:     errs,
		Action:     middleware.SignInPath,
		Stylesheet: view.URL(view.Stylesheet),
		HTMX:       view.URL("htmx.min.js"),
	})
}

// doLogin authenticates and rotates the session.
func (m *Module) doLogin(w http.ResponseWriter, r *http.Request) {
	in := LoginRequest{
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
	}
	if errs := in.Validate(); errs.Any() {
		m.renderLogin(w, r, http.StatusUnprocessableEntity, in.Email, errs)
		return
	}

	// The tenant comes from the application, never from the request: a header or
	// a form field here would let anyone pick which tenant to authenticate
	// against.
	tenant := m.tenant(r)

	// The address the attempt came from, read from the socket and never from a
	// header: X-Forwarded-For is written by whoever is calling, so keying the
	// throttle on it would let an attacker reset their own counter every request.
	u, err := m.svc.Authenticate(r.Context(), tenant, in.Email, in.Password, middleware.KeyByIP(r))
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			// One sentence for a missing account and a wrong password, on the
			// field the person can see. Naming which half was wrong is the
			// account enumeration oracle ErrInvalidCredentials exists to close.
			m.renderLogin(w, r, http.StatusUnauthorized, in.Email, validation.Errors{
				"email": {"invalid email or password"},
			})
			return
		}
		// The lockout is a refusal, not a failure of this application: answering
		// it with 500 would send somebody who typed their password wrong five
		// times to the error page.
		var locked TooManyAttemptsError
		if errors.As(err, &locked) {
			w.Header().Set("Retry-After", strconv.Itoa(locked.Seconds()))
			m.renderLogin(w, r, http.StatusTooManyRequests, in.Email, validation.Errors{
				"email": {fmt.Sprintf("too many attempts, try again in %d seconds", locked.Seconds())},
			})
			return
		}
		observability.Log(r.Context()).Error("login failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Rotating the id is mandatory here: keeping the pre-login session is
	// session fixation.
	old := m.svc.session.IDFromRequest(r)
	if _, err := m.svc.session.Rotate(r.Context(), w, old, SubjectOf(u)); err != nil {
		observability.Log(r.Context()).Error("starting session", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Where they were going before the guard turned them away, and the front
	// page when there was nowhere in particular. The destination is validated as
	// local by the store, which is why this line can be one line: an unchecked
	// one would be an open redirect on the one screen every application has.
	redirect(w, r, m.svc.session.TakeIntended(w, r, "/"))
}

// doLogout destroys the session on the server, not only in the browser.
func (m *Module) doLogout(w http.ResponseWriter, r *http.Request) {
	if err := m.svc.session.Destroy(r.Context(), w, m.svc.session.IDFromRequest(r)); err != nil {
		observability.Log(r.Context()).Error("destroying session", "error", err)
	}
	redirect(w, r, middleware.SignInPath)
}

// redirect answers the way the client can act on.
//
// Setting HX-Redirect and 200 unconditionally is fine for HTMX and broken for
// everything else: a form posted without JavaScript -- a browser with scripts
// off, a crawler, curl -- gets 200 with an empty body and stays on a blank page,
// signed in, with no way to know where to go. The rule lives in one shape, and
// this calls it.
//
// It stays a function rather than the call being inlined at nine call sites,
// because the name is what says these handlers redirect the same way the rest of
// the framework does.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	fhttp.Redirect(w, r, to)
}

// The sign-in screen the framework ships, for a project that has not published
// the starter kit yet.
//
// It is one screen and it stays one: `go run github.com/arandu-io/ui@latest auth`
// replaces it with nine, in kyse, that the project then owns. What this one has
// to be is not impressive -- it has to be indistinguishable from the rest of the
// application, because the alternative is a person building a styled landing
// page, clicking Sign in, and landing on unstyled black-on-white with no
// navigation. That reads as a broken deploy, and it is the first thing anybody
// sees.
//
// So it loads the application's own stylesheet and carries the same two body
// attributes every other page carries:
//
//   - hx-boost, or following a link here does a full page load and the
//     application stops feeling like one;
//   - hx-headers, or every HTMX request that changes state fails the CSRF check,
//     which is the single most common mistake in this stack.
//
// # Why the form opts out of hx-boost
//
// Because a wrong password is a 401, and htmx swaps no 4xx: its response
// handling is `{code:"[45]..", swap:false, error:true}` in the copy this
// framework embeds. A boosted form posting to this handler therefore answered
// every refusal -- wrong password, empty field, lockout -- by leaving the screen
// exactly as it was. The person typed, pressed the button, and nothing happened
// at all, which reads as the application being broken rather than as the
// password being wrong.
//
// The guards' fix does not apply here: HX-Refresh reloads, and a reload of the
// sign-in screen throws away both the message and the address that was typed.
// So the form posts natively -- a full navigation, exactly what a browser with
// scripts off already does -- and the answer is this same page with the error on
// it, in every client, at the status the handler chose. The hidden _csrf field
// is what carries the token on that post, which is why it is in the markup
// beside the hx-headers attribute rather than instead of it.
//
// # Why it carries its own layout CSS
//
// It cannot use a Tailwind utility class. Not "should not" -- cannot.
//
// The stylesheet is compiled with `@import "tailwindcss" source(none)` and an
// explicit `@source` list naming the project's views. This markup lives in the
// framework, in the module cache, and is never scanned. A utility written here
// is a class that exists in the HTML and in no stylesheet on earth.
//
// The shape of that failure: `max-w-sm` and `justify-center` do nothing, so the
// form runs the full width of the window while the button beside it looks
// correct -- because `.btn` is `@layer components`, which Tailwind emits whether
// anything scanned uses it or not. Half-styled reads worse than unstyled: it
// looks like the CSS half-loaded.
//
// So the split is by where the rule comes from:
//
//   - `.field`, `.label`, `.input`, `.btn` -- the component layer, always
//     emitted, so this screen looks like the rest of the application;
//   - the layout below -- this screen's own, in a <style> block, so it is
//     correct even in a project that replaced the stylesheet or has none.
//
// The same shape as observability/errorpage, and for the same reason: a page the
// framework has to be able to draw cannot depend on the application's build.
//
// # Why that <style> is inside <body>
//
// Because hx-boost swaps the body and the title, and throws the rest of the
// response away. A <style> in <head> arrives at the browser and is discarded, so
// the screen is styled on a direct load and unstyled the moment somebody reaches
// it by clicking a link -- which is how everybody reaches it.
//
// It is the same failure twice, from opposite ends: the utility classes were
// absent from a stylesheet that loaded, and this was present in a response that
// was half-read. Both look like a broken deploy and neither shows up in a diff.
//
// A <style> inside <body> is not conformant HTML and every browser has honoured
// it for twenty years. The conformant alternatives are worse: an inline style
// attribute on each of the eight elements, or a second stylesheet at a second
// URL.
var loginForm = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in</title>
<meta name="robots" content="noindex">
<link rel="stylesheet" href="{{.Stylesheet}}">
<script src="{{.HTMX}}" defer></script>
</head>
<body hx-boost="true" hx-headers='{"X-CSRF-Token": "{{.CSRFToken}}"}'>
<style>
  html { min-height: 100%; }
  body {
    margin: 0; min-height: 100vh;
    display: flex; align-items: center; justify-content: center;
    background: var(--background, #fff); color: var(--foreground, #111);
    font-family: var(--font-sans, ui-sans-serif, system-ui, sans-serif);
  }
  .signin { width: 100%; max-width: 22rem; padding: 2rem 1.5rem; }
  .signin h1 { margin: 0; font-size: 1.5rem; font-weight: 600; letter-spacing: -0.02em; }
  .signin p { margin: 0.5rem 0 0; font-size: 0.875rem; color: var(--muted-foreground, #666); }
  .signin form { margin-top: 2rem; display: flex; flex-direction: column; gap: 1rem; }
  .signin button { margin-top: 0.5rem; }
  .signin .alert { margin-top: 1.5rem; }
</style>
<main class="signin">
  <h1>Sign in</h1>
  <p>Use the address this application knows you by.</p>
{{if .Errors}}
  <div class="alert" role="alert" data-variant="destructive">
    <h2>That did not work</h2>
    <section><ul>{{range $field, $msgs := .Errors}}{{range $msgs}}<li>{{$field}}: {{.}}</li>{{end}}{{end}}</ul></section>
  </div>
{{end}}
  <form method="post" action="{{.Action}}" hx-boost="false">
    <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
    <div class="field">
      <label class="label" for="email">Email</label>
      <input class="input" type="email" id="email" name="email" value="{{.Email}}" autocomplete="username" required autofocus>
    </div>
    <div class="field">
      <label class="label" for="password">Password</label>
      <input class="input" type="password" id="password" name="password" autocomplete="current-password" required>
    </div>
    <button class="btn" type="submit">Sign in</button>
  </form>
</main>
</body></html>`))
