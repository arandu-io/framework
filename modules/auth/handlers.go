package auth

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/validation"
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
	_ = loginForm.Execute(w, map[string]any{"CSRFToken": token})
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

	// Phase 2 resolves the tenant from the host name. Until then it is a header,
	// which is fine because the tenant of an authenticated request comes from the
	// session, not from here.
	tenant := r.Header.Get("X-Tenant")

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

	// HX-Redirect makes HTMX navigate without a full page reload.
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

// doLogout destroys the session on the server, not only in the browser.
func (m *Module) doLogout(w http.ResponseWriter, r *http.Request) {
	if err := m.svc.session.Destroy(r.Context(), w, m.svc.session.IDFromRequest(r)); err != nil {
		observability.Log(r.Context()).Error("destroying session", "error", err)
	}
	w.Header().Set("HX-Redirect", "/auth/login")
	w.WriteHeader(http.StatusOK)
}

func renderLoginError(w http.ResponseWriter, status int, errs validation.Errors) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginErrors.Execute(w, errs)
}

// The markup below is the minimum needed for phase 1 to authenticate. The real
// view layer is templ, in phase 2 -- see docs/03-roadmap-fases.md. Note the
// hx-headers attribute: without it every HTMX request that changes state fails
// the CSRF check, which is the single most common mistake in this stack.
var loginForm = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Sign in</title></head>
<body hx-headers='{"X-CSRF-Token": "{{.CSRFToken}}"}'>
<form method="post" action="/auth/login">
  <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
  <label>Email <input type="email" name="email" autocomplete="username" required></label>
  <label>Password <input type="password" name="password" autocomplete="current-password" required></label>
  <button type="submit">Sign in</button>
</form>
</body></html>`))

var loginErrors = template.Must(template.New("loginErrors").Parse(
	`<ul>{{range $field, $msgs := .}}{{range $msgs}}<li>{{$field}}: {{.}}</li>{{end}}{{end}}</ul>`))
