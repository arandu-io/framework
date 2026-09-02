package feature

import (
	stdhtml "html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/html"
	hesapemiddleware "github.com/arandu-io/hesape/http/middleware"
)

var appKey = []byte("0123456789abcdef0123456789abcdef")

func csrfHandler(sessionID string) (http.Handler, *security.CSRF) {
	csrf := security.NewCSRF(appKey, time.Hour)
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	})
	h := fhttp.Chain(reached, middleware.CSRFProtect(csrf, func(*http.Request) string { return sessionID }))
	return h, csrf
}

func TestCSRFAcceptsTheHeader(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	r.Header.Set("X-CSRF-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid header token", rec.Code)
	}
}

// TestCSRFAcceptsTheFormField covers the plain HTML form, which must keep working
// for anyone who is not using HTMX.
func TestCSRFAcceptsTheFormField(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", strings.NewReader("_token="+token))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid form token", rec.Code)
	}
}

// tokenField renders the hidden field a form ships with, exactly as the form
// builder writes it, and reads the name and the value back off the markup.
//
// Reading the name off the markup rather than writing it out here is the whole
// point of the test below: the name is the one thing the builder and this
// middleware have to agree on, and a test that spells it a second time only
// ever agrees with itself.
//
// The builder is given no URL generator because this field needs none -- a
// hidden input resolves no address -- and no session, because the token is
// passed to the constructor instead.
func tokenField(t *testing.T, token string) (name, value string) {
	t.Helper()

	field, err := html.NewFormBuilder(html.NewHtmlBuilder(nil), nil, token).Token()
	if err != nil {
		t.Fatalf("FormBuilder.Token: %v", err)
	}

	attributes := map[string]string{}
	for _, pair := range attributePattern.FindAllStringSubmatch(string(field), -1) {
		attributes[pair[1]] = pair[2]
	}

	name, value = attributes["name"], attributes["value"]
	if name == "" || value != token {
		t.Fatalf("read %q=%q off %q, want the token under a name of its own", name, value, field)
	}
	return name, value
}

// attributePattern reads one name="value" pair off a tag.
var attributePattern = regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`)

// TestCSRFAcceptsTheFieldAFormActuallyCarries submits what the form builder
// writes, rather than a field name this file spells for a second time.
//
// The builder and this middleware are in different modules, and each was
// internally consistent about a different name: the builder wrote the field the
// session keys the token under, and the middleware read another one. Every form
// built that way was refused as though it carried no token at all, and no test
// on either side could see it -- both posted their own spelling and read it
// back.
func TestCSRFAcceptsTheFieldAFormActuallyCarries(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	name, value := tokenField(t, token)
	r := httptest.NewRequest(http.MethodPost, "/invoices",
		strings.NewReader(url.Values{name: {value}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the form carried %q and this middleware reads another name, "+
			"so every form the builder writes is refused", rec.Code, name)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	h, _ := csrfHandler("session-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/invoices", nil))

	if rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d", rec.Code, middleware.StatusCSRFExpired)
	}
}

// TestCSRFRejectsATokenFromAnotherSession is the property that makes
// double-submit worth anything.
func TestCSRFRejectsATokenFromAnotherSession(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	stolen, err := csrf.Issue("session-2")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	r.Header.Set("X-CSRF-Token", stolen)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want the token to be refused", rec.Code)
	}
}

// The token is not the only thing that decides any more. A page on another site
// that got hold of a valid token used to be enough on its own, and the browser
// says where the request came from long before the HMAC is computed.
func TestCSRFRefusesACrossSiteRequestCarryingAValidToken(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, tc := range []struct{ name, header, value string }{
		{"the browser reports another site", "Sec-Fetch-Site", "cross-site"},
		{"the browser reports a sibling host", "Sec-Fetch-Site", "same-site"},
		{"an older browser sends only Origin", "Origin", "https://attacker.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
			r.Header.Set("X-CSRF-Token", token)
			r.Header.Set(tc.header, tc.value)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: the token was valid and the request was not ours", rec.Code)
			}
			if rec.Code == middleware.StatusCSRFExpired {
				t.Error("a cross-origin request was answered as an expired token, so the page would reload into the same refusal")
			}
			if !strings.Contains(rec.Body.String(), "cross-origin") {
				t.Errorf("the refusal does not say what was wrong: %q", rec.Body.String())
			}
		})
	}
}

// The other half: the application's own pages must keep working, whichever of
// the two headers the browser sends.
func TestCSRFLetsTheApplicationsOwnPagesThrough(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, tc := range []struct{ name, header, value string }{
		{"the browser reports the same origin", "Sec-Fetch-Site", "same-origin"},
		{"the browser reports no origin at all", "Sec-Fetch-Site", "none"},
		{"an older browser sends a matching Origin", "Origin", "https://example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
			r.Header.Set("X-CSRF-Token", token)
			r.Header.Set(tc.header, tc.value)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: this is the site posting to itself", rec.Code)
			}
		})
	}
}

func TestCSRFLetsSafeMethodsThrough(t *testing.T) {
	h, _ := csrfHandler("session-1")

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/invoices", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200: a read must not need a token", method, rec.Code)
		}
	}
}

// TestCSRFGuardsEveryWriteMethod: protecting POST only is a common and expensive
// mistake, because HTMX issues PUT, PATCH and DELETE directly.
func TestCSRFGuardsEveryWriteMethod(t *testing.T) {
	h, _ := csrfHandler("session-1")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/invoices", nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s passed without a token", method)
		}
	}
}

// An expired token reaches a person as nothing at all under HTMX: htmx does not
// swap a 419, so the sentence telling them to reload was in a body that was
// thrown away, and the button they pressed simply did nothing.
//
// HX-Refresh reloads the page, which is the only useful reaction to a token that
// is no longer valid -- the reload brings a fresh one. The role guard answers the
// same way for the same reason; changing one of the two would have left the
// framework refusing an HTMX request in two different shapes.
func TestAnExpiredTokenTellsHtmxToReloadRatherThanLeavingTheScreenUnchanged(t *testing.T) {
	h, _ := csrfHandler("session-1")

	for name, r := range map[string]*http.Request{
		"no token at all": httptest.NewRequest(http.MethodPost, "/invoices", nil),
		"a token from another session": httptest.NewRequest(http.MethodPost, "/invoices",
			strings.NewReader("_token=not-this-sessions-token")),
	} {
		t.Run(name, func(t *testing.T) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("HX-Request", "true")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Header().Get("HX-Refresh") != "true" {
				t.Error("htmx was given nothing to do, so the form did nothing and said nothing")
			}
			if rec.Code != middleware.StatusCSRFExpired {
				t.Errorf("status = %d, want %d unchanged", rec.Code, middleware.StatusCSRFExpired)
			}
		})
	}
}

// A form posted without JavaScript already renders the body, and must not be
// told to reload: the reload would replace the explanation with the page they
// were on.
func TestAPlainFormPostIsNotToldToReload(t *testing.T) {
	h, _ := csrfHandler("session-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/invoices", nil))

	if got := rec.Header().Get("HX-Refresh"); got != "" {
		t.Errorf("HX-Refresh = %q on a request no htmx sent", got)
	}
	// And it has to name the field a form actually carries. A refusal that
	// spells it any other way is the first thing somebody reads when a form
	// fails, and it sends them to add a field nothing here reads.
	name, _ := tokenField(t, "a-token-this-test-does-not-submit")
	if !strings.Contains(rec.Body.String(), name) {
		t.Errorf("the refusal does not name %q, the field a form carries: %q", name, rec.Body.String())
	}
}

// TestAFormSubmissionIsCheckedBeforeItsMethodIsOverridden exercises the public
// seam from FormBuilder's HTML to the final HTTP handler. The simulated browser
// reads its method, target and fields from the markup, so this test cannot stay
// green when a writer and a reader merely repeat the same field name.
func TestAFormSubmissionIsCheckedBeforeItsMethodIsOverridden(t *testing.T) {
	const sessionID = "session-1"
	csrf := security.NewCSRF(appKey, time.Hour)
	token, err := csrf.Issue(sessionID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			form := html.NewFormBuilder(html.NewHtmlBuilder(formURLStub{}), formURLStub{}, token)
			markup, err := form.Open(html.OpenOptions{Method: method, URL: []string{"/invoices/42"}})
			if err != nil {
				t.Fatalf("FormBuilder.Open: %v", err)
			}

			r := nativeFormRequest(t, string(markup))
			if r.Method != http.MethodPost {
				t.Fatalf("browser method = %s, want POST from the form markup", r.Method)
			}

			checkedMethod := ""
			reachedMethod := ""
			reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reachedMethod = r.Method
				w.WriteHeader(http.StatusNoContent)
			})
			h := fhttp.Chain(reached,
				middleware.CSRFProtect(csrf, func(r *http.Request) string {
					checkedMethod = r.Method
					return sessionID
				}),
				hesapemiddleware.OverrideMethod(),
			)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 from the handler", rec.Code)
			}
			if checkedMethod != http.MethodPost {
				t.Errorf("CSRF checked method = %s, want the POST the browser sent", checkedMethod)
			}
			if reachedMethod != method {
				t.Errorf("handler method = %s, want %s from the form's hidden field", reachedMethod, method)
			}
		})
	}
}

var formTagPattern = regexp.MustCompile(`<form\b[^>]*>`)
var inputTagPattern = regexp.MustCompile(`<input\b[^>]*>`)

func nativeFormRequest(t *testing.T, markup string) *http.Request {
	t.Helper()

	formTag := formTagPattern.FindString(markup)
	if formTag == "" {
		t.Fatalf("no form tag in %q", markup)
	}
	formAttributes := tagAttributes(formTag)
	method := strings.ToUpper(formAttributes["method"])
	if method == "" {
		method = http.MethodGet
	}
	action := stdhtml.UnescapeString(formAttributes["action"])

	fields := url.Values{}
	for _, inputTag := range inputTagPattern.FindAllString(markup, -1) {
		attributes := tagAttributes(inputTag)
		if name := stdhtml.UnescapeString(attributes["name"]); name != "" {
			fields.Add(name, stdhtml.UnescapeString(attributes["value"]))
		}
	}

	r := httptest.NewRequest(method, action, strings.NewReader(fields.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func tagAttributes(tag string) map[string]string {
	attributes := map[string]string{}
	for _, pair := range attributePattern.FindAllStringSubmatch(tag, -1) {
		attributes[pair[1]] = pair[2]
	}
	return attributes
}

type formURLStub struct{}

func (formURLStub) To(path string, _ ...string) string                { return path }
func (formURLStub) Secure(path string, _ ...string) string            { return path }
func (formURLStub) Asset(path string) string                          { return path }
func (formURLStub) SecureAsset(path string) string                    { return path }
func (formURLStub) Route(name string, _ ...string) (string, error)    { return name, nil }
func (formURLStub) Action(action string, _ ...string) (string, error) { return action, nil }
func (formURLStub) Current() string                                   { return "/" }
