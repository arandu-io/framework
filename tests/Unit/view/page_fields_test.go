package unit

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/auth"
	hhttp "github.com/arandu-io/hesape/http"
	hview "github.com/arandu-io/hesape/view"
)

// TestPageHasExactlyTheFieldsOfHesapesPage holds this Page to hesape's, field
// for field: same names, same types, same order.
//
// New copies hesape's page into this one by name, so a field hesape adds does
// not stop anybody's build -- and for the same reason it would not appear here
// either, and the page would quietly stop carrying something the layout reads.
// This is where that drift is caught: on the change that requires the hesape
// release that added it, with the missing field named.
func TestPageHasExactlyTheFieldsOfHesapesPage(t *testing.T) {
	ours := reflect.TypeFor[view.Page]()
	theirs := reflect.TypeFor[hview.Page]()

	for i := 0; i < max(ours.NumField(), theirs.NumField()); i++ {
		var o, h string
		if i < ours.NumField() {
			f := ours.Field(i)
			o = f.Name + " " + f.Type.String()
		}
		if i < theirs.NumField() {
			f := theirs.Field(i)
			h = f.Name + " " + f.Type.String()
		}
		if o != h {
			t.Errorf("field %d: framework view.Page has %q, hesape view.Page has %q", i, o, h)
		}
	}
}

// TestNewCopiesEveryFieldHesapeFills: with every field of hesape's page set,
// the page New returns carries each one. A field the copy forgets is the
// zero value here, named by the test, rather than a blank in a layout.
func TestNewCopiesEveryFieldHesapeFills(t *testing.T) {
	operator := security.Subject{ID: "op-1", Tenant: "acme"}
	acting, err := auth.ImpersonateActing(operator, security.Subject{ID: "user-9", Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}

	var ours view.Page
	var theirs hview.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/panel", func(ctx *fhttp.Context) error {
		ours = view.New(ctx, "Panel")
		theirs = hview.New(ctx, "Panel")
		return nil
	})
	nothing := func(http.ResponseWriter, *http.Request) {}
	r.Get("/{$}", nothing).Name("home")
	r.Get("/auth/login", nothing).Name("auth.login")
	r.Post("/auth/logout", nothing).Name("auth.logout")
	r.Get("/auth/register", nothing).Name("auth.register")
	r.Post("/impersonate/leave", nothing).Name("impersonate.leave")

	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	ctx := auth.WithSubject(req.Context(), acting)
	ctx = hhttp.WithCSRFToken(ctx, "issued-token")
	ctx = hhttp.WithAppName(ctx, "Example")
	ctx = fhttp.WithState(ctx, fhttp.State{
		Errors:   validation.Errors{"title": {"is required"}},
		Old:      url.Values{"title": {"A draft"}},
		Messages: map[string]string{"status": "Saved."},
	})
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	// Compared by name rather than by converting one to the other: a
	// conversion stops this file compiling when the two drift, and the test
	// above is the one that names the field.
	o, h := reflect.ValueOf(ours), reflect.ValueOf(theirs)
	for i := range h.NumField() {
		name := h.Type().Field(i).Name
		got := o.FieldByName(name)
		if !got.IsValid() {
			t.Errorf("view.Page has no field %s", name)
			continue
		}
		if !reflect.DeepEqual(got.Interface(), h.Field(i).Interface()) {
			t.Errorf("New dropped %s: got %#v, hesape filled %#v", name, got.Interface(), h.Field(i).Interface())
		}

		// Every field hesape's New fills from the request has to be set here,
		// or the comparison proved nothing about its copy.
		switch name {
		case "Description", "Canonical", "UserName", "PanelURL", "AdminURL":
			continue // the controller's to fill; New leaves them empty
		}
		if h.Field(i).IsZero() {
			t.Errorf("hesape's New left %s empty, so this test does not cover its copy", name)
		}
	}
}
