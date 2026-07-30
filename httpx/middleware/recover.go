// Package middleware holds the mandatory request pipeline.
//
// Order matters and is not a matter of taste: Recover must be the outermost
// middleware, or a panic raised in any other middleware escapes without a page;
// Observe must come right after it, because everything below depends on the
// context it builds.
package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/observability/errorpage"
)

// Recover captures panics and decides what to render.
//
// In development: the full debug page -- stack, request, queries, dumps.
// Anywhere else: a bare 500 that leaks nothing, carrying the request id so the
// operator can correlate it with the structured log.
func Recover(dev bool, opts errorpage.Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Being outermost has a consequence: the Collector is created by
			// Observe, further in, so the context here is the one from before it
			// existed. The slot is the reference the panic path reads it through.
			// It is installed in development only, which is also the only place
			// the debug page may exist.
			if dev {
				r = r.WithContext(observability.WithCollectorSlot(r.Context()))
			}

			defer func() {
				v := recover()
				if v == nil {
					return
				}
				// A client that hung up mid-response is not an application bug,
				// and net/http expects this value to keep unwinding.
				if v == http.ErrAbortHandler {
					panic(v)
				}

				ctx := r.Context()
				col := observability.FromContext(ctx)
				log := observability.Log(ctx)

				if observability.IsDumpDie(v) {
					if dev {
						errorpage.RenderDump(w, r, col, opts)
						return
					}
					// DumpDie is a no-op outside development, so arriving here
					// means the sentinel came from somewhere else.
					log.Error("dump-and-die sentinel outside development")
				}

				log.Error("recovered panic", "panic", v)

				if dev {
					errorpage.Render(w, r, v, col, opts)
					return
				}

				// The id comes from the response header rather than the
				// Collector: production has no Collector, and this is the only
				// thread back to the log line for this request.
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal error. request_id: " + w.Header().Get("X-Request-ID")))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
