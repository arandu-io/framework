package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
)

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

func TestRouterMatchesMethodAndPath(t *testing.T) {
	r := httpx.NewRouter()
	r.Get("/users", ok)
	r.Post("/users", ok)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/users", http.StatusOK},
		{http.MethodPost, "/users", http.StatusOK},
		{http.MethodDelete, "/users", http.StatusMethodNotAllowed},
		{http.MethodGet, "/unknown", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestGroupPrefixes(t *testing.T) {
	r := httpx.NewRouter()
	api := r.Group("/api")
	v1 := api.Group("/v1")
	v1.Get("/health", ok)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: nested groups must concatenate prefixes", rec.Code)
	}
}

func TestPathParameters(t *testing.T) {
	r := httpx.NewRouter()
	r.Get("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(req.PathValue("id")))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/u-42", nil))

	if got := rec.Body.String(); got != "u-42" {
		t.Fatalf("path value = %q, want u-42", got)
	}
}

// TestMiddlewareOrder pins the contract: the first middleware in the list is the
// outermost, so the pipeline order is the order of execution.
func TestMiddlewareOrder(t *testing.T) {
	var order []string
	mark := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "in:"+name)
				next.ServeHTTP(w, r)
				order = append(order, "out:"+name)
			})
		}
	}

	h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}), mark("first"), mark("second"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	got := strings.Join(order, ",")
	want := "in:first,in:second,handler,out:second,out:first"
	if got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

// TestGroupMiddlewareIsInherited: a group is how a module protects a whole area
// at once, so inheritance is the property that matters.
func TestGroupMiddlewareIsInherited(t *testing.T) {
	r := httpx.NewRouter()
	blocked := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
	}
	admin := r.Group("/admin", blocked)
	admin.Group("/reports").Get("/monthly", ok)
	r.Get("/public", ok)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/reports/monthly", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("nested group status = %d, want 403: middleware must be inherited", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/public", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sibling route status = %d, want 200: group middleware must not leak", rec.Code)
	}
}

func TestRoutesMetadataCarriesTheModule(t *testing.T) {
	r := httpx.NewRouter()
	auth := r.ForModule("auth").Group("/auth")
	auth.Get("/login", ok)
	auth.Post("/login", ok)
	r.ForModule("billing").Get("/invoices", ok)

	routes := r.Routes()

	if len(routes) != 3 {
		t.Fatalf("registered %d routes, want 3", len(routes))
	}
	byPattern := map[string]httpx.Route{}
	for _, rt := range routes {
		byPattern[rt.Method+" "+rt.Pattern] = rt
	}
	if got := byPattern["GET /auth/login"].Module; got != "auth" {
		t.Errorf("module of GET /auth/login = %q, want auth", got)
	}
	if got := byPattern["GET /invoices"].Module; got != "billing" {
		t.Errorf("module of GET /invoices = %q, want billing", got)
	}
}

func TestRootAndTrailingSlashJoin(t *testing.T) {
	r := httpx.NewRouter()
	r.Group("/").Get("/", ok)
	r.Group("/api/").Get("v1", ok)

	for _, path := range []string{"/", "/api/v1"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}
