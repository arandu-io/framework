package unit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/auth"
)

// pageFor draws a page through New on a router that names the way back out of
// an impersonation only when leave is true, for a request carrying subject.
func pageFor(t *testing.T, subject *security.Subject, leave bool) view.Page {
	t.Helper()

	var page view.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/panel", func(ctx *fhttp.Context) error {
		page = view.New(ctx, "Panel")
		return nil
	})
	if leave {
		r.Post("/impersonate/leave", func(http.ResponseWriter, *http.Request) {}).Name("impersonate.leave")
	}

	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	if subject != nil {
		req = req.WithContext(auth.WithSubject(req.Context(), *subject))
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return page
}

// TestNewSaysAnOperatorIsImpersonatingSomebody: the page this package's New
// returns carries the impersonation exactly as hesape's New fills it, for an
// operator acting, an operator only looking, and an ordinary session.
func TestNewSaysAnOperatorIsImpersonatingSomebody(t *testing.T) {
	operator := security.Subject{ID: "op-1", Tenant: "acme"}
	customer := security.Subject{ID: "user-9", Tenant: "acme"}

	acting, err := auth.ImpersonateActing(operator, customer)
	if err != nil {
		t.Fatal(err)
	}
	p := pageFor(t, &acting, true)
	if !p.Impersonating || !p.ImpersonationActs || p.Impersonator != "op-1" || p.LeaveImpersonationURL != "/impersonate/leave" {
		t.Fatalf("an acting operator's page reads impersonating=%v acts=%v impersonator=%q leave=%q",
			p.Impersonating, p.ImpersonationActs, p.Impersonator, p.LeaveImpersonationURL)
	}

	viewing, err := auth.Impersonate(operator, customer)
	if err != nil {
		t.Fatal(err)
	}
	if p := pageFor(t, &viewing, false); !p.Impersonating || p.ImpersonationActs || p.LeaveImpersonationURL != "" {
		t.Fatalf("a viewing page without the route reads %+v", p)
	}

	if p := pageFor(t, &customer, true); p.Impersonating || p.Impersonator != "" || p.LeaveImpersonationURL != "" {
		t.Fatalf("an ordinary page reads as an impersonation: %+v", p)
	}
}

// TestMarshalJSONCarriesTheImpersonation: the four fields describe the session
// of whoever reads the page, to that reader, so the allow-list names them.
func TestMarshalJSONCarriesTheImpersonation(t *testing.T) {
	page := filled()
	page.Impersonating = true
	page.ImpersonationActs = true
	page.Impersonator = "op-1"
	page.LeaveImpersonationURL = "/impersonate/leave"

	out, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshalling the page: %v", err)
	}
	for _, want := range []string{
		`"Impersonating":true`, `"ImpersonationActs":true`,
		`"Impersonator":"op-1"`, `"LeaveImpersonationURL":"/impersonate/leave"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the allow-list dropped %s:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), liveToken) {
		t.Fatalf("the CSRF token reached the output:\n%s", out)
	}
}
