package view_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/view"
)

// homeData is the shape a view declares in its @go block, and the shape the
// controller has to pass. A different type is an error naming both sides.
type homeData struct{ Name string }

func init() {
	// What `aru view:build` emits for resources/views/home.kyse.go.
	view.Register("home", func(w io.Writer, data any) error {
		d, ok := data.(homeData)
		if !ok {
			return view.WrongData("home", "homeData", data)
		}
		_, err := io.WriteString(w, "<h1>Olá "+d.Name+"</h1>")
		return err
	})
}

// TestTheControllerRendersByName is the whole chain: a controller action calls
// ctx.View with a name and a typed struct, and HTML comes out.
//
// It goes through the router rather than calling the renderer directly, because
// the wiring is the part that breaks -- the assets shipped 404 for weeks
// precisely because every test called the handler and none called the route.
func TestTheControllerRendersByName(t *testing.T) {
	r := httpx.NewRouter().WithRenderer(view.NewRenderer())
	r.Action(http.MethodGet, "/", func(ctx *httpx.Context) error {
		return ctx.View("home", homeData{Name: "Paulo"})
	}).Name("home")

	server := httptest.NewServer(r)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := string(body); got != "<h1>Olá Paulo</h1>" {
		t.Errorf("body = %q", got)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestAViewWithNoRendererSaysWhatToWire: without the view module registered,
// ctx.View has nothing to render with. The message has to name the line that is
// missing from main.go, because the alternative is a nil dereference pointing at
// the framework.
func TestAViewWithNoRendererSaysWhatToWire(t *testing.T) {
	r := httpx.NewRouter() // no WithRenderer
	r.Action(http.MethodGet, "/", func(ctx *httpx.Context) error {
		return ctx.View("home", homeData{})
	})

	server := httptest.NewServer(r)
	defer server.Close()

	// The message is what matters, so it is read from the error rather than
	// from the 500 the server answers.
	ctx := &httpx.Context{Response: httptest.NewRecorder(), Request: httptest.NewRequest("GET", "/", nil)}
	err := ctx.View("home", homeData{})
	if err == nil {
		t.Fatal("rendering without a renderer succeeded")
	}
	if !strings.Contains(err.Error(), "main.go") {
		t.Errorf("the error does not name where to wire it: %v", err)
	}
}

// TestAnUnknownViewSuggests: a missing view is a typo, a file never built, or a
// name that drifted. Listing the near misses answers all three faster than the
// name alone.
func TestAnUnknownViewSuggests(t *testing.T) {
	err := view.NewRenderer().Render(context.Background(), httptest.NewRecorder(),
		http.StatusOK, "hom", nil)
	if err == nil {
		t.Fatal("an unknown view rendered")
	}
	if !strings.Contains(err.Error(), "home") {
		t.Errorf("the error does not suggest the near miss: %v", err)
	}
}

// TestWrongDataNamesBothSides: the controller and the view disagreeing about
// the data is the one failure a name-based render can still have. Rendering the
// zero value instead would be a blank page with a 200.
func TestWrongDataNamesBothSides(t *testing.T) {
	err := view.NewRenderer().Render(context.Background(), httptest.NewRecorder(),
		http.StatusOK, "home", "isto nao e homeData")
	if err == nil {
		t.Fatal("the wrong data type rendered")
	}
	for _, want := range []string{"homeData", "string"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestTheRenderIsOnTheTimeline: "the page is slow because of the database" and
// "because of the markup" are two different afternoons, and the console is what
// tells them apart.
func TestTheRenderIsOnTheTimeline(t *testing.T) {
	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	if err := view.NewRenderer().Render(ctx, httptest.NewRecorder(),
		http.StatusOK, "home", homeData{Name: "x"}); err != nil {
		t.Fatal(err)
	}

	renders := col.Renders()
	if len(renders) != 1 || renders[0].Name != "home" {
		t.Fatalf("the render was not recorded: %+v", renders)
	}
}

// TestAFragmentCarriesItsStatus: a form that failed validation answers 422 with
// the form fragment, and HTMX swaps it in. Answering 200 would make the browser
// and the logs both believe it worked.
func TestAFragmentCarriesItsStatus(t *testing.T) {
	r := httpx.NewRouter().WithRenderer(view.NewRenderer())
	r.Action(http.MethodPost, "/", func(ctx *httpx.Context) error {
		return ctx.Fragment(http.StatusUnprocessableEntity, "home", homeData{Name: "erro"})
	})

	server := httptest.NewServer(r)
	defer server.Close()

	resp, err := http.Post(server.URL+"/", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status %d, want 422", resp.StatusCode)
	}
}
