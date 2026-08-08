package auth

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
)

// Handlers are thin on purpose: extract the input, delegate to the service,
// render. No business rule and no repository access lives here -- `aru doctor`
// complains when a handler imports the data package.

// showLogin renders the login form with a fresh CSRF token.
func (m *Module) showLogin(w http.ResponseWriter, r *http.Request) {
	token, err := m.svc.csrf.Issue(m.svc.session.IDFromRequest(r))
	if err != nil {
		observability.Log(r.Context()).Error("issuing csrf token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginForm.Execute(w, map[string]any{
		"CSRFToken": token,
		// Both are content-addressed, so they are read here rather than written
		// as constants: the URL changes with the bytes, and a hard-coded one
		// would serve last build's stylesheet forever.
		"Stylesheet": view.URL(view.Stylesheet),
		"HTMX":       view.URL("htmx.min.js"),
	})
}

// doLogin authenticates and rotates the session.
func (m *Module) doLogin(w http.ResponseWriter, r *http.Request) {
	in := LoginRequest{
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
	}
	if errs := in.Validate(); errs.Any() {
		// HTMX: answer with the form partial and its inline errors.
		renderLoginError(w, http.StatusUnprocessableEntity, errs)
		return
	}

	// The tenant comes from the application, never from the request: a header or
	// a form field here would let anyone pick which tenant to authenticate
	// against. Phase 2 adds a resolver that reads the host name.
	tenant := m.tenant(r)

	u, err := m.svc.Authenticate(r.Context(), tenant, in.Email, in.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			renderLoginError(w, http.StatusUnauthorized, validation.Errors{
				"email": {"invalid email or password"},
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

	redirect(w, r, "/")
}

// doLogout destroys the session on the server, not only in the browser.
func (m *Module) doLogout(w http.ResponseWriter, r *http.Request) {
	if err := m.svc.session.Destroy(r.Context(), w, m.svc.session.IDFromRequest(r)); err != nil {
		observability.Log(r.Context()).Error("destroying session", "error", err)
	}
	redirect(w, r, "/auth/login")
}

// redirect answers the way the client can act on.
//
// It used to set HX-Redirect and 200 unconditionally, and that is fine for HTMX
// and broken for everything else: a form posted without JavaScript -- a browser
// with scripts off, a crawler, curl -- got 200 with an empty body and stayed on
// a blank page, signed in, with no way to know where to go.
//
// The rule is the one httpx.Context.Redirect already applies, and having it in
// two shapes is what let one of them be wrong. It lives here rather than being
// borrowed because these handlers take a raw http.ResponseWriter, and wrapping
// one in a Context to reach a two-line branch would be the more surprising code.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func renderLoginError(w http.ResponseWriter, status int, errs validation.Errors) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginErrors.Execute(w, errs)
}

// The sign-in screen the framework ships, for a project that has not published
// the starter kit yet.
//
// It is one screen and it stays one: `go run github.com/arandu-io/ui@latest auth`
// replaces it with nine, in kyse, that the project then owns (ADR 0026). What
// this one has to be is not impressive -- it has to be indistinguishable from
// the rest of the application, because the alternative is what it used to be: a
// person builds a styled landing page, clicks Sign in, and lands on unstyled
// black-on-white with no navigation. That reads as a broken deploy, and it was
// the first thing anybody saw.
//
// So it loads the application's own stylesheet and carries the same two body
// attributes every other page carries:
//
//   - hx-boost, or following a link here does a full page load and the
//     application stops feeling like one;
//   - hx-headers, or every HTMX request that changes state fails the CSRF check,
//     which is the single most common mistake in this stack.
//
// # Why it carries its own layout CSS
//
// It cannot use a Tailwind utility class. Not "should not" -- cannot.
//
// The stylesheet is compiled with `@import "tailwindcss" source(none)` and an
// explicit `@source` list (ADR 0025), and that list names the project's views.
// This markup lives in the framework, in the module cache, and is never scanned.
// A utility written here is a class that exists in the HTML and in no stylesheet
// on earth.
//
// It went out that way once: `max-w-sm` and `justify-center` did nothing, so the
// form ran the full width of the window while the button beside it looked
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
// URL, which is the thing RULE 9 exists to refuse.
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
</style>
<main class="signin">
  <h1>Sign in</h1>
  <p>Use the address this application knows you by.</p>

  <form method="post" action="/auth/login">
    <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
    <div class="field">
      <label class="label" for="email">Email</label>
      <input class="input" type="email" id="email" name="email" autocomplete="username" required autofocus>
    </div>
    <div class="field">
      <label class="label" for="password">Password</label>
      <input class="input" type="password" id="password" name="password" autocomplete="current-password" required>
    </div>
    <button class="btn" type="submit">Sign in</button>
  </form>
</main>
</body></html>`))

var loginErrors = template.Must(template.New("loginErrors").Parse(
	`<div class="alert" role="alert" data-variant="destructive">
<h2>That did not work</h2>
<section><ul>{{range $field, $msgs := .}}{{range $msgs}}<li>{{$field}}: {{.}}</li>{{end}}{{end}}</ul></section>
</div>`))
