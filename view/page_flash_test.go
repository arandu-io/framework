package view_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
)

func TestPageCarriesTheErrorsOfTheRequestThatRedirectedHere(t *testing.T) {
	state := fhttp.State{
		Errors: validation.Errors{"password": {"must be at least 12 characters"}},
		Old:    url.Values{"email": {"ada@example.test"}},
	}

	var page view.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/signup", func(ctx *fhttp.Context) error {
		page = view.New(ctx, "Sign up").WithToken("csrf-token")
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/signup", nil)
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(fhttp.WithState(req.Context(), state)))

	// Nothing in the handler mentioned errors, and the errors are on the page.
	if got := page.FieldError("password"); got != "must be at least 12 characters" {
		t.Errorf("FieldError = %q", got)
	}
	if got := page.OldValue("email"); got != "ada@example.test" {
		t.Errorf("OldValue = %q", got)
	}
	if !page.HasErrors() {
		t.Error("HasErrors said no on a page that was redirected here from a rejection")
	}
	// The chrome the constructor is also responsible for.
	if page.Title != "Sign up" || page.Path != "/signup" || page.CSRFToken() != "csrf-token" {
		t.Errorf("chrome = %q %q %q", page.Title, page.Path, page.CSRFToken())
	}
}

func TestAPageNobodyWasRejectedOnDrawsNothing(t *testing.T) {
	var page view.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/signup", func(ctx *fhttp.Context) error {
		page = view.New(ctx, "Sign up")
		return nil
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/signup", nil))

	// A screen asks unconditionally, so all three have to answer without a nil
	// check at the call site.
	if page.HasErrors() {
		t.Error("HasErrors said yes on a fresh page")
	}
	if got := page.FieldError("email"); got != "" {
		t.Errorf("FieldError = %q", got)
	}
	if got := page.OldValue("email"); got != "" {
		t.Errorf("OldValue = %q", got)
	}
	if got := page.ErrorSummary(); got != nil {
		t.Errorf("ErrorSummary = %v, want nil so the banner is not drawn empty", got)
	}
}

func TestOldValueIsEmptyForAPasswordField(t *testing.T) {
	// The value never reaches the page, because it never reaches the flash. The
	// message does, and that is the distinction the whole design turns on: an
	// empty password box that does not say why it was rejected is the failure
	// being fixed.
	page := view.Page{
		Errors: validation.Errors{"password": {"must be at least 12 characters"}},
		Old:    url.Values{"email": {"ada@example.test"}},
	}

	if got := page.OldValue("password"); got != "" {
		t.Errorf("OldValue(password) = %q", got)
	}
	if got := page.FieldError("password"); got == "" {
		t.Error("the password box would come back empty and silent")
	}
}

func TestOldOrKeepsTheRejectedEditRatherThanTheStoredValue(t *testing.T) {
	page := view.Page{Old: url.Values{"title": {"A better title"}, "subtitle": {""}}}

	if got := page.OldOr("title", "The stored title"); got != "A better title" {
		t.Errorf("OldOr = %q: an edit form that reverts undoes what somebody is in the middle of writing", got)
	}
	// Deliberately cleared, and it must stay cleared. Falling back here would
	// refill a box somebody emptied on purpose.
	if got := page.OldOr("subtitle", "The stored subtitle"); got != "" {
		t.Errorf("OldOr on a cleared field = %q, want empty", got)
	}
	// Nothing was flashed for it at all.
	if got := page.OldOr("body", "The stored body"); got != "The stored body" {
		t.Errorf("OldOr fallback = %q", got)
	}
}

func TestErrorSummaryNamesTheFieldAndTheBareMessageDoesNot(t *testing.T) {
	page := view.Page{Errors: validation.Errors{
		"password_confirmation": {"does not match"},
		"email":                 {"is not a valid email address"},
	}}

	// Sorted, so the banner does not reshuffle between two renders of the same
	// failure.
	//
	// "Password Confirmation" and not "Password confirmation": the name comes
	// from validation.Humanize, which is now hesape/str.Headline, and Headline
	// title cases every word where this project sentence cased the first. The
	// sentence casing was an Arandu divergence from Str::headline and the
	// validation bridge closed it deliberately -- see the doc comment on
	// validation.Humanize. It changes the text of a banner, never whether a
	// form passes, and a one-word field reads identically, which is most of them.
	want := []string{
		"Email is not a valid email address",
		"Password Confirmation does not match",
	}
	if got := page.ErrorSummary(); !reflect.DeepEqual(got, want) {
		t.Errorf("ErrorSummary =\n%q\nwant\n%q", got, want)
	}

	// The same message drawn under its own labelled box says only what to
	// change. The name is prepended for the banner and baked into nothing.
	if got := page.FieldError("email"); got != "is not a valid email address" {
		t.Errorf("FieldError = %q, want the bare sentence", got)
	}
}

func TestPageStillSatisfiesLayout(t *testing.T) {
	// The compile-time assertion in page.go is the real check; this names it so
	// that a build failure there points at a test with a reason in it. Adding
	// HasErrors and ErrorSummary to Layout is what makes the banner the layout's
	// -- and every screen in every project gets them by embedding Page, which is
	// what keeps that addition from being a change each of them has to make.
	var _ view.Layout = view.Page{}

	page := view.Page{Errors: validation.Errors{"email": {"is required"}}}
	var layout view.Layout = page
	if !layout.HasErrors() || len(layout.ErrorSummary()) != 1 {
		t.Error("a layout cannot see that this page was rejected")
	}
}
