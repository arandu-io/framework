// The router documentation examples, compiled.
//
// Router.Action and Router.Resource each carried an example spelled against
// Route, which is an alias for the route metadata type and has neither method.
// Both were published to the reference for as long as they stood, because a
// doc comment is text and nothing in the build reads it. These are the same
// lines as functions, so the compiler reads them now, and the lines on
// Singleton, ResourceAction, Invokable and a nested Resource are here for the
// same reason.

package http_test

import (
	"fmt"
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
)

// invoices implements three of the seven resource actions, which is what
// Resource registers routes for.
type invoices struct{}

func (invoices) Index(*fhttp.Context) error { return nil }
func (invoices) Show(*fhttp.Context) error  { return nil }
func (invoices) Store(*fhttp.Context) error { return nil }

func ExampleRouter_Get() {
	r := fhttp.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}).Name("health")

	path, err := r.Table().URL("health")
	fmt.Println(path, err)
	// Output: /health <nil>
}

func ExampleRouter_Action() {
	r := fhttp.NewRouter()
	r.Action("GET", "/dashboard", func(*fhttp.Context) error { return nil }).Name("dashboard")

	path, err := r.Table().URL("dashboard")
	fmt.Println(path, err)
	// Output: /dashboard <nil>
}

func ExampleRouter_Resource() {
	r := fhttp.NewRouter()
	routes := r.Resource("invoices", invoices{})

	for _, route := range routes {
		fmt.Println(route.RouteName())
	}
	// Output:
	// invoices.index
	// invoices.store
	// invoices.show
}

func ExampleRouter_Group() {
	r := fhttp.NewRouter()
	admin := r.Group("/admin")
	admin.Action("GET", "/comments", func(*fhttp.Context) error { return nil }).Name("admin.comments")

	path, err := r.Table().URL("admin.comments")
	fmt.Println(path, err)
	// Output: /admin/comments <nil>
}

// tasks lists the tasks of a project and shows one.
type tasks struct{}

func (tasks) Index(*fhttp.Context) error { return nil }
func (tasks) Show(*fhttp.Context) error  { return nil }

func ExampleRouter_Resource_nested() {
	r := fhttp.NewRouter()
	r.Resource("projects.tasks", tasks{})

	list, _ := r.Table().URL("projects.tasks.index", "3")
	one, _ := r.Table().URL("projects.tasks.show", "9")
	fmt.Println(list)
	fmt.Println(one)
	// Output:
	// /projects/3/tasks
	// /tasks/9
}

// settings shows and updates the one record there is.
type settings struct{}

func (settings) Show(*fhttp.Context) error   { return nil }
func (settings) Update(*fhttp.Context) error { return nil }

func ExampleRouter_Singleton() {
	r := fhttp.NewRouter()
	for _, route := range r.Singleton("settings", settings{}) {
		fmt.Println(route.RouteName())
	}

	path, err := r.Table().URL("settings.show")
	fmt.Println(path, err)
	// Output:
	// settings.show
	// settings.update
	// /settings <nil>
}

func ExampleRouter_ResourceAction() {
	r := fhttp.NewRouter()
	publish := func(*fhttp.Context) error { return nil }
	r.ResourceAction(http.MethodPost, "notes", "publish", publish)

	path, err := r.Table().URL("notes.publish", "7")
	fmt.Println(path, err)
	// Output: /notes/7/publish <nil>
}

// export is a controller with one action.
type export struct{}

func (export) Invoke(*fhttp.Context) error { return nil }

func ExampleRouter_Invokable() {
	r := fhttp.NewRouter()
	r.Invokable(http.MethodPost, "/exports", export{}).Name("exports.store")

	path, err := r.Table().URL("exports.store")
	fmt.Println(path, err)
	// Output: /exports <nil>
}
