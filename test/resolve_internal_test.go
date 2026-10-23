package test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/hesape/routing"
)

// Resolve is the browser.Resolver a page visits route names with, so it must
// keep satisfying that type and build the same path Route builds.
func TestResolveIsTheBrowserResolverRouteBuilds(t *testing.T) {
	r := routing.NewRouter()
	r.Get("/invoices/{id}", http.NotFoundHandler()).Name("invoices.show")
	s := &Server{t: t, routes: r.Table()}

	var resolve browser.Resolver = s.Resolve
	got, err := resolve("invoices.show", 42)
	if err != nil {
		t.Fatal(err)
	}
	if want := s.Route("invoices.show", "42"); got != want {
		t.Errorf("Resolve built %q, Route built %q", got, want)
	}
	if _, err := resolve("nowhere"); err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("Resolve of an unknown route answered %v, want an error naming it", err)
	}
}
