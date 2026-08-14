// The page itself, answered by github.com/arandu-io/hesape/exception.
//
// Neither entry point is an alias, and neither could be: hesape draws the two
// pages from unexported methods on its Handler, so what is left is to build the
// Handler these two arguments describe and reach the method through the one
// exported door that leads to it. Render goes through the debug displayer;
// RenderDump goes through Recover, which is the only caller of the dump page
// there is.

package errorpage

import (
	"context"
	"fmt"
	"net/http"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/exception"
	hlog "github.com/arandu-io/hesape/log"
)

// Options carries what the page needs from the application configuration.
//
// It stays declared here rather than aliasing hesape/exception.Config, which
// carries five fields more: Dev, Views, DontReport, RenderJSONWhen and Console.
// Aliasing would put all five on a struct the skeletons construct as a literal
// and would move the meaning of Dev into a package whose absolute rule is that
// it is unreachable outside development. The translation is below, and it is
// three fields wide.
type Options struct {
	// Editor is the target of the "open in IDE" links: vscode, cursor, goland
	// or zed.
	Editor string
	// AppModule is the module path of the application, used to tell app frames
	// from framework and stdlib frames.
	AppModule string
	// Diagnose collects what the registered modules have to say about the state
	// of the system right now. Pass kernel.Diagnose.
	//
	// It exists because the most useful hint is often about something that
	// happened outside this request: the outbox has been stuck for four minutes,
	// the scheduler last ran an hour ago. A page that only looks at the request
	// cannot see any of it, and that is exactly the state where somebody is
	// staring at an error wondering what changed.
	Diagnose func(ctx context.Context) []string
}

// handler builds the hesape handler the two entry points draw through.
//
// Dev is true and not a parameter, because this package has one rule and it is
// that nothing in it is reachable when Env is not dev. Both functions below
// drew the debug page unconditionally before the move, and the caller that
// decides is middleware.Recover.
func (o Options) handler() *exception.Handler {
	return exception.NewHandler(exception.Config{
		Dev:       true,
		Editor:    o.Editor,
		AppModule: o.AppModule,
		Diagnose:  o.Diagnose,
	})
}

// collected puts the caller's Collector where hesape reads one.
//
// The old signature takes it as an argument and the new code reads it off the
// request context, because the Handler is called from places that have no
// argument to pass. Installing it is enough to make the two agree: FromContext
// prefers the collector over the slot, so a request that already carries one
// keeps whatever the caller decided.
func collected(r *http.Request, col *observability.Collector) *http.Request {
	if col == nil {
		return r
	}
	return r.WithContext(observability.WithCollector(r.Context(), col))
}

// Render draws the full error page.
//
// It is hesape/exception's debug displayer, which is the exported door to the
// page. Three things about that are worth knowing before reading a page drawn
// through here:
//
//   - The status is the failure's answer rather than a fixed 500. A panic
//     nothing classifies is still 500; one carrying a status -- an Abort, or one
//     of the sentinels hesape knows -- is now drawn with that status instead of
//     being reported to the client as an internal error.
//   - The stack has three more collapsed frames on top: this function, the
//     deferred recover that called it, and runtime.gopanic. hesape captures
//     inside the displayer, which is one hop further out than the old capture
//     from inside this function. The line that panicked is still on the stack
//     and still the first expanded frame.
//   - A panic value that is not an error is wrapped, so the subtitle -- the Go
//     type of what failed -- reads as the wrapper rather than as the original.
//     The headline and the message are unchanged, because both are built from
//     the text. hesape has no exported entry that takes the raw value.
func Render(w http.ResponseWriter, r *http.Request, panicValue any, col *observability.Collector, opts Options) {
	err, ok := panicValue.(error)
	if !ok {
		err = panicked{value: panicValue}
	}
	opts.handler().Displayer().Display(w, collected(r, col), err)
}

// panicked carries a panic value that was not an error.
//
// hesape draws the page from an error, and a panic in Go is any value at all.
// Error() is what the headline and the message are built from, so both read
// exactly as they did; the Go type on the page is this type's, which is the one
// thing the wrapper costs.
type panicked struct{ value any }

func (p panicked) Error() string { return fmt.Sprint(p.value) }

// RenderDump draws the dump page, for the DumpDie flow.
//
// The dump page is drawn by an unexported method whose one caller is Recover,
// on the branch that recognises the dump-and-die sentinel. So this is Recover,
// handed a handler that raises that sentinel: hesape catches it, sees
// development, and draws the page. It is indirect and it is the whole of the
// exported surface that reaches this page -- see the note in the report.
//
// Two details keep it honest. The sentinel is unexported in hesape/log and
// DumpDie is the only door to it, so DumpDie is what raises it; the context
// passed carries no Collector, so its recording half does nothing and no
// phantom entry is added to the page the caller is about to see. And Recover
// answers the dump page before it logs anything, so nothing is written to the
// log that the old path did not write.
func RenderDump(w http.ResponseWriter, r *http.Request, col *observability.Collector, opts Options) {
	die := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hlog.DumpDie(context.Background(), "", nil)
	})
	exception.Recover(opts.handler())(die).ServeHTTP(w, collected(r, col))
}
