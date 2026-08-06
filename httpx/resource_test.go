package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
)

// invoices implements all seven actions, so Resource registers all seven.
type invoices struct{ seen *[]string }

func (c invoices) note(action string) error { *c.seen = append(*c.seen, action); return nil }

func (c invoices) Index(*httpx.Context) error   { return c.note("index") }
func (c invoices) Create(*httpx.Context) error  { return c.note("create") }
func (c invoices) Store(*httpx.Context) error   { return c.note("store") }
func (c invoices) Show(*httpx.Context) error    { return c.note("show") }
func (c invoices) Edit(*httpx.Context) error    { return c.note("edit") }
func (c invoices) Update(*httpx.Context) error  { return c.note("update") }
func (c invoices) Destroy(*httpx.Context) error { return c.note("destroy") }

// TestResourceRegistersTheSevenOfLaravel is the whole point of the command: a
// developer arriving from Laravel writes Route.Resource and gets the same seven
// routes, at the same paths, with the same names.
//
// If any row of this table drifts, the promise in RULE 10 -- "he should
// recognize the vocabulary immediately" -- stops being true.
func TestResourceRegistersTheSevenOfLaravel(t *testing.T) {
	r := httpx.NewRouter()
	var seen []string
	r.Resource("invoices", invoices{seen: &seen})

	want := []struct {
		method, pattern, name string
	}{
		{"GET", "/invoices", "invoices.index"},
		{"GET", "/invoices/create", "invoices.create"},
		{"POST", "/invoices", "invoices.store"},
		{"GET", "/invoices/{id}", "invoices.show"},
		{"GET", "/invoices/{id}/edit", "invoices.edit"},
		{"PUT", "/invoices/{id}", "invoices.update"},
		{"DELETE", "/invoices/{id}", "invoices.destroy"},
	}

	got := map[string]string{} // "METHOD pattern" -> name
	for _, route := range r.Routes() {
		got[route.Method+" "+route.Pattern] = route.RouteName()
	}

	for _, w := range want {
		key := w.method + " " + w.pattern
		name, registered := got[key]
		if !registered {
			t.Errorf("%s was not registered", key)
			continue
		}
		if name != w.name {
			t.Errorf("%s is named %q, want %q", key, name, w.name)
		}
	}

	// PATCH answers update too, like Laravel, and shares its name so URL
	// generation has one answer instead of two.
	if _, ok := got["PATCH /invoices/{id}"]; !ok {
		t.Error("PATCH /invoices/{id} was not registered")
	}
}

// listOnly implements two of the seven. Laravel registers all seven regardless
// and 500s on the missing ones; here a route that exists is a route that answers.
type listOnly struct{}

func (listOnly) Index(*httpx.Context) error { return nil }
func (listOnly) Show(*httpx.Context) error  { return nil }

func TestResourceRegistersOnlyWhatTheControllerImplements(t *testing.T) {
	r := httpx.NewRouter()
	r.Resource("reports", listOnly{})

	for _, route := range r.Routes() {
		switch route.RouteName() {
		case "reports.index", "reports.show":
		default:
			t.Errorf("registered %s %s (%s) for a controller that does not implement it",
				route.Method, route.Pattern, route.RouteName())
		}
	}
	if n := len(r.Routes()); n != 2 {
		t.Errorf("registered %d routes, want 2", n)
	}
}

// TestTheResourceRoutesActuallyAnswer: registering metadata is not the same as
// wiring a handler, and a table test alone would not tell them apart.
func TestTheResourceRoutesActuallyAnswer(t *testing.T) {
	r := httpx.NewRouter()
	var seen []string
	r.Resource("invoices", invoices{seen: &seen})

	server := httptest.NewServer(r)
	defer server.Close()

	for _, call := range []struct{ method, path, action string }{
		{"GET", "/invoices", "index"},
		{"GET", "/invoices/create", "create"},
		{"POST", "/invoices", "store"},
		{"GET", "/invoices/42", "show"},
		{"GET", "/invoices/42/edit", "edit"},
		{"PUT", "/invoices/42", "update"},
		{"DELETE", "/invoices/42", "destroy"},
	} {
		seen = nil
		req, _ := http.NewRequest(call.method, server.URL+call.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", call.method, call.path, err)
		}
		_ = resp.Body.Close()

		if len(seen) != 1 || seen[0] != call.action {
			t.Errorf("%s %s reached %v, want [%s]", call.method, call.path, seen, call.action)
		}
	}
}

// TestCreateIsNotReadAsAnID is the ambiguity every router has to resolve:
// /invoices/create and /invoices/{id} both match "create".
func TestCreateIsNotReadAsAnID(t *testing.T) {
	r := httpx.NewRouter()
	var seen []string
	r.Resource("invoices", invoices{seen: &seen})

	server := httptest.NewServer(r)
	defer server.Close()

	resp, err := http.Get(server.URL + "/invoices/create")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(seen) != 1 || seen[0] != "create" {
		t.Fatalf("GET /invoices/create reached %v, want [create] -- it was read as an id", seen)
	}
}

// TestURLIsGeneratedFromTheName is what replaces "/invoices/"+id at the call
// site. A hardcoded path keeps compiling after the route moves; this does not.
func TestURLIsGeneratedFromTheName(t *testing.T) {
	r := httpx.NewRouter()
	r.Get("/", func(http.ResponseWriter, *http.Request) {}).Name("home")
	r.Resource("invoices", invoices{seen: new([]string)})

	cases := []struct {
		name   string
		params []string
		want   string
	}{
		{"home", nil, "/"},
		{"invoices.index", nil, "/invoices"},
		{"invoices.create", nil, "/invoices/create"},
		{"invoices.show", []string{"42"}, "/invoices/42"},
		{"invoices.edit", []string{"42"}, "/invoices/42/edit"},
	}
	for _, c := range cases {
		got, err := r.Table().URL(c.name, c.params...)
		if err != nil {
			t.Errorf("URL(%q): %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestABrokenURLSaysWhat: the error is what a person reads at 3am, so it names
// the route and what to do.
func TestABrokenURLSaysWhat(t *testing.T) {
	r := httpx.NewRouter()
	r.Resource("invoices", invoices{seen: new([]string)})

	if _, err := r.Table().URL("invoices.list"); err == nil {
		t.Error("an unknown name was accepted")
	} else if !strings.Contains(err.Error(), "aru routes") {
		t.Errorf("the error does not say how to find the real name: %v", err)
	}

	if _, err := r.Table().URL("invoices.show"); err == nil {
		t.Error("a route needing an id was built without one")
	} else if !strings.Contains(err.Error(), "{id}") {
		t.Errorf("the error does not name the missing parameter: %v", err)
	}

	if _, err := r.Table().URL("invoices.index", "42"); err == nil {
		t.Error("a route taking no parameter accepted one")
	}
}

// TestTwoRoutesCannotShareAName: with two, every URL built from it goes to one
// of them, chosen by registration order. Finding that out at boot beats finding
// it out from a link that quietly points at the wrong page.
func TestTwoRoutesCannotShareAName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("two routes shared a name and nothing complained")
		}
	}()

	r := httpx.NewRouter()
	r.Get("/a", func(http.ResponseWriter, *http.Request) {}).Name("dup")
	r.Get("/b", func(http.ResponseWriter, *http.Request) {}).Name("dup")
}
