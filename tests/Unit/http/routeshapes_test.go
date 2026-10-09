// The route shapes hesape/routing takes an adapter for, reached through the
// framework's Router.
//
// The shapes themselves -- the paths, the names, the shallow nesting, the
// refused GET -- are tested in hesape against the code that registers them.
// What is tested here is the one thing this package adds: each method forwards
// with the framework's adapter, so an action's error is answered the way Action
// answers one. A problem document is the proof, because hesape/routing has no
// adapter and writes none.

package unit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
)

// settingsController implements the three actions a singleton registers, and
// Index and Destroy, which a singleton does not.
type settingsController struct{ seen *[]string }

func (c settingsController) Index(*fhttp.Context) error {
	*c.seen = append(*c.seen, "index")
	return nil
}
func (c settingsController) Show(*fhttp.Context) error { return security.ErrForbidden }
func (c settingsController) Edit(*fhttp.Context) error {
	*c.seen = append(*c.seen, "edit")
	return nil
}
func (c settingsController) Update(*fhttp.Context) error {
	*c.seen = append(*c.seen, "update")
	return nil
}
func (c settingsController) Destroy(*fhttp.Context) error {
	*c.seen = append(*c.seen, "destroy")
	return nil
}

// do sends one request and answers the recorder.
func do(r http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

var wantsJSON = http.Header{"Accept": {"application/json"}}

func TestASingletonForwardsWithTheFrameworkAdapter(t *testing.T) {
	var seen []string
	r := fhttp.NewRouter()
	routes := r.Singleton("settings", settingsController{seen: &seen})

	var names []string
	for _, route := range routes {
		names = append(names, route.RouteName())
	}
	if got := strings.Join(names, " "); got != "settings.show settings.edit settings.update" {
		t.Fatalf("Singleton registered %q, want show, edit and update", got)
	}
	if path, err := r.Table().URL("settings.edit"); err != nil || path != "/settings/edit" {
		t.Errorf("settings.edit = %q, %v; want /settings/edit, no id", path, err)
	}

	if rec := do(r, http.MethodPatch, "/settings", nil); rec.Code != http.StatusOK || strings.Join(seen, " ") != "update" {
		t.Errorf("PATCH /settings = %d reaching %v, want 200 reaching update", rec.Code, seen)
	}
	rec := do(r, http.MethodGet, "/settings", wantsJSON)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("show returned ErrForbidden and was answered %d, want 403", rec.Code)
	}
	problemOf(t, rec)
}

func TestAResourceActionTakesTheMethodFirst(t *testing.T) {
	var gotID, gotTask string
	tagged := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Route-Middleware", "ran")
			next.ServeHTTP(w, req)
		})
	}

	r := fhttp.NewRouter()
	r.ResourceAction(http.MethodPost, "notes", "publish", func(ctx *fhttp.Context) error {
		gotID = ctx.Param("id")
		return nil
	}, tagged)
	r.ResourceAction(http.MethodPost, "projects.tasks", "close", func(ctx *fhttp.Context) error {
		gotTask = ctx.Param("task")
		return database.ErrRecordNotFound
	})

	if path, err := r.Table().URL("notes.publish", "7"); err != nil || path != "/notes/7/publish" {
		t.Errorf("notes.publish = %q, %v; want /notes/7/publish", path, err)
	}
	rec := do(r, http.MethodPost, "/notes/7/publish", nil)
	if rec.Code != http.StatusOK || gotID != "7" {
		t.Errorf("POST /notes/7/publish = %d with id %q, want 200 with 7", rec.Code, gotID)
	}
	if rec.Header().Get("X-Route-Middleware") != "ran" {
		t.Error("the route's own middleware did not run")
	}

	rec = do(r, http.MethodPost, "/tasks/9/close", wantsJSON)
	if gotTask != "9" || rec.Code != http.StatusNotFound {
		t.Fatalf("POST /tasks/9/close = %d with task %q, want 404 with 9", rec.Code, gotTask)
	}
	problemOf(t, rec)

	defer func() {
		if msg, _ := recover().(string); !strings.Contains(msg, "POST, PUT, PATCH or DELETE") {
			t.Fatalf("a GET resource action panicked with %q, want the refusal naming the methods", msg)
		}
	}()
	r.ResourceAction(http.MethodGet, "notes", "preview", func(*fhttp.Context) error { return nil })
	t.Fatal("a resource action registered with GET")
}

// exportController is a single-action controller.
type exportController struct{ status int }

func (c exportController) Invoke(*fhttp.Context) error {
	if c.status == 0 {
		return nil
	}
	return domainError{status: c.status}
}

func TestAnInvokableControllerTakesTheMethodFirst(t *testing.T) {
	r := fhttp.NewRouter()
	r.Invokable(http.MethodPost, "/exports", exportController{}).Name("exports.store")
	r.Invokable("put", "/exports/locked", exportController{status: http.StatusConflict})

	if path, err := r.Table().URL("exports.store"); err != nil || path != "/exports" {
		t.Errorf("exports.store = %q, %v; want /exports", path, err)
	}
	if rec := do(r, http.MethodPost, "/exports", nil); rec.Code != http.StatusOK {
		t.Errorf("POST /exports = %d, want 200", rec.Code)
	}
	if rec := do(r, http.MethodGet, "/exports", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /exports = %d, want 405: the route answers the one method it was given", rec.Code)
	}

	rec := do(r, http.MethodPut, "/exports/locked", wantsJSON)
	if rec.Code != http.StatusConflict {
		t.Fatalf("an Invoke declaring 409 was answered %d", rec.Code)
	}
	problemOf(t, rec)
}

// A dot in the name nests the resource, and the framework's Resource forwards
// the name as it is, so the nesting reaches a controller written against this
// package: the collection under the parent, the record at its own path.
func TestADottedResourceNestsThroughTheFrameworkRouter(t *testing.T) {
	c := &taskController{}
	r := fhttp.NewRouter()
	r.Resource("projects.tasks", c)

	for _, tc := range []struct{ name, arg, want string }{
		{"projects.tasks.index", "3", "/projects/3/tasks"},
		{"projects.tasks.show", "9", "/tasks/9"},
	} {
		if path, err := r.Table().URL(tc.name, tc.arg); err != nil || path != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.name, path, err, tc.want)
		}
	}

	do(r, http.MethodGet, "/projects/3/tasks", nil)
	do(r, http.MethodGet, "/tasks/9", nil)
	if c.project != "3" || c.task != "9" {
		t.Errorf("index read project %q and show read task %q, want 3 and 9", c.project, c.task)
	}
}

// taskController lists the tasks of a project and shows one task.
type taskController struct{ project, task string }

func (c *taskController) Index(ctx *fhttp.Context) error {
	c.project = ctx.Param("project")
	return nil
}

func (c *taskController) Show(ctx *fhttp.Context) error {
	c.task = ctx.Param("task")
	return nil
}
