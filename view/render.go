package view

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/observability"
)

// Func is what a compiled view is: a function that writes HTML.
//
// `aru view:build` emits one per `.kyse.go` and registers it by name. The data
// arrives as `any` and the generated function asserts it back to the struct the
// view declared -- which is why a wrong type is an error naming both sides
// rather than a blank page.
type Func func(w io.Writer, data any) error

var (
	mu    sync.RWMutex
	views = map[string]Func{}
)

// Register records a compiled view under the name a controller renders it by.
//
//	view.Register("invoices/index", renderInvoicesIndex)
//
// Generated code calls it from init(), so importing the views package is what
// makes them reachable -- the same shape as a database/sql driver.
//
// Registering twice panics rather than replacing. Two views for one name is a
// build artifact that outlived its source, and finding out at boot beats
// finding out from a page that renders the wrong thing.
func Register(name string, f Func) {
	mu.Lock()
	defer mu.Unlock()
	if _, taken := views[name]; taken {
		panic("view: " + name + " is already registered -- a stale generated file is probably still on disk")
	}
	views[name] = f
}

// Registered returns the known view names, sorted. `aru doctor` reads it to
// check that every ctx.View("x") has a view named x.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(views))
	for name := range views {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Renderer draws a view. It is the concrete side of httpx.Renderer.
type Renderer struct{}

// NewRenderer returns the renderer. The kernel hands it to the router at boot.
func NewRenderer() *Renderer { return &Renderer{} }

// Render writes a named view to the response.
//
// The render is recorded on the Collector, so a slow page shows on the console
// whether the time went to the database or to the markup -- which is the
// difference between two very different afternoons.
func (*Renderer) Render(ctx context.Context, w http.ResponseWriter, status int, name string, data any) error {
	mu.RLock()
	f, known := views[name]
	mu.RUnlock()

	if !known {
		return unknownView(name)
	}

	// The page is drawn into a buffer, and only then written.
	//
	// Rendering straight into the ResponseWriter commits the status with the
	// first byte, so a view that fails halfway answers 200 with half a page --
	// the error page cannot change a status already on the wire, and monitoring
	// sees a success. It showed up as a type mismatch between a view and its
	// layout that reached the browser as a 200.
	//
	// The cost is holding one page in memory. That is what html/template's own
	// documentation recommends, and what any engine that renders to a string does:
	// streaming and reporting errors are not both available.
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	start := time.Now()
	err := f(buf, data)
	observability.FromContext(ctx).RecordRender(name, time.Since(start))
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(status)
	_, err = w.Write(buf.Bytes())
	return err
}

// bufferPool keeps the per-page buffer off the allocator's back. A page is a few
// kilobytes and every request needs one.
var bufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// unknownView names the fix instead of the failure.
//
// A missing view is almost always one of three things: a typo, a file that was
// never built, or a name that drifted from the path. Listing the near misses
// answers all three faster than the name alone.
func unknownView(name string) error {
	known := Registered()

	var near []string
	for _, k := range known {
		if strings.Contains(k, name) || strings.Contains(name, k) {
			near = append(near, k)
		}
	}

	switch {
	case len(near) > 0:
		return fmt.Errorf("view: no view named %q. Did you mean %s?", name, strings.Join(near, ", "))
	case len(known) == 0:
		return fmt.Errorf("view: no view named %q, and none are registered at all. "+
			"Run `aru view:build`, and import the views package in bootstrap/app.go", name)
	default:
		return fmt.Errorf("view: no view named %q. Registered: %s", name, strings.Join(known, ", "))
	}
}

// WrongData is what a generated view returns when the data is not the struct it
// declared.
//
// Generated code calls it, so the message is the same everywhere:
//
//	d, ok := data.(HomeData)
//	if !ok { return view.WrongData("home", "HomeData", data) }
//
// The alternative -- rendering the zero value -- is a blank page with a 200,
// which is the failure this framework exists to make impossible.
func WrongData(view, want string, got any) error {
	return fmt.Errorf("view %q takes %s and got %T. The controller and the view disagree about the data", view, want, got)
}

// RenderToString draws a view and returns it, instead of writing it to a
// response.
//
// It exists for mail, and for anything else that produces HTML nobody is
// waiting on a socket for. The Renderer above cannot serve that: it takes an
// http.ResponseWriter and sets a status, and a message being composed in a job
// has neither.
//
// It is the same registry and the same compiled function, which is the point:
// an e-mail is drawn by the view layer a page is drawn by, so a field that does
// not exist is a compile error there too, and interpolation is escaped by
// construction rather than by whoever wrote the template.
func (*Renderer) RenderToString(name string, data any) (string, error) {
	mu.RLock()
	f, known := views[name]
	mu.RUnlock()

	if !known {
		return "", unknownView(name)
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	if err := f(buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
