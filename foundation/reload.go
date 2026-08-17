package foundation

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	fhttp "github.com/arandu-io/framework/http"
)

// Live reload: the browser follows the restart, in development only.
//
// # This file is a copy, and the reason is visibility
//
// hesape/foundation/reload.go holds the same code, and this is not a second way
// to do one thing -- it is the same way, written twice, because the first copy
// is out of reach.
//
// That package exports exactly one symbol from the file, ReloadTagger, and this
// package aliases it. Everything the reload actually needs -- devReload,
// liveReload, htmlRecorder and its five methods, newBootID -- is unexported, and
// so is the whole of hesape/foundation/internal.go, which is where
// internalPrefix, exceptInternal and requireTracingSecret restated below come
// from. Go has no way to reach any of them from another module.
//
// The signature difference between []fhttp.Middleware and
// []pipeline.Middleware[http.Handler] is not the obstacle: those two are the
// same type, because fhttp.Middleware is an alias. The obstacle is the
// lower-case letter, and no aliasing removes it.
//
// The way out is for hesape/foundation to export what a kernel above it needs,
// and that is a change to a published module rather than a rewrite here. Until
// it happens the copy stays, with this note, so that nobody deletes it believing
// an alias will do.
//
// # Why it asks rather than listens
//
// This was an EventSource, and holding the connection open was the defect.
//
// HTTP/1.1 allows a browser six connections per origin, and a held stream
// spends one. On navigation the browser abandons the stream of the page it is
// leaving without closing it, and the server only notices when its next write
// fails -- up to the keepalive interval later. Six quick navigations left six
// dead streams holding every slot, and the seventh click did nothing at all
// until one of them timed out. Reported as "I navigate, I go back, I navigate,
// and it freezes", which is exactly what it was.
//
// Asking every second costs one connection for a millisecond and cannot
// accumulate. It also removed the keepalive ticker, the flush, the streaming
// content type, and the twenty seconds Shutdown used to spend waiting for a
// request that never ended.
//
// # Why the signal is the connection and not a message
//
// `aru dev` restarts the process on every change, so anything the old process
// knew is gone before it could announce anything. There is no moment at which a
// "reload now" message could be sent -- the sender is already dead.
//
// What survives is the browser's end of the connection. EventSource reconnects
// on its own, and the identity of what it reconnects TO is the whole signal:
// every process picks a random boot id at start, so an id that differs from the
// one the page loaded with means a different process is answering, which means
// the code changed. A dropped Wi-Fi connection reconnecting to the same process
// reports the same id and reloads nothing.
//
// That makes the mechanism a consequence of how `aru dev` already works rather
// than a protocol between two programs, which is the difference between one
// thing to keep working and two.
//
// # Why it is a middleware and not a tag in the layout
//
// A tag in the layout is a line every project has to carry, in a file people
// edit, that does nothing in production and looks like it should be deleted. The
// middleware mounts with the route, under the same IsDev, and a project that
// never heard of any of this gets it.
//
// It injects only into a document that has a </body>. An HTMX fragment does not,
// so a swap never receives a second EventSource -- which would otherwise
// accumulate one connection per interaction until the browser's six-per-origin
// limit closed the page down.
//
// # Why the script is a file and not inline
//
// The CSP is script-src 'self', and an inline <script> is refused by it -- the
// browser would drop the tag silently and the feature would appear simply not
// to work. So the script is registered as an ordinary asset and referenced by
// src, content-addressed like every other one. It costs one request, on a
// development server, once per page.
//
// # What is deliberately not here
//
// No CSS hot-swap, no state preservation, no module replacement. Those need a
// client-side runtime and a module graph, which is the JavaScript build this
// framework refuses. A full reload against a Go server that restarts in under a
// second is a fair trade, and it is one behaviour rather than two.

// reloadPath is where the browser listens. Under _arandu with health and the
// console, so one prefix covers everything the framework mounts.
const reloadPath = internalPrefix + "reload"

// bootID identifies this process to a page that was loaded by another one.
//
// Random rather than a timestamp or a pid: two processes started in the same
// second, or a pid the kernel recycled, would report the same identity and the
// page would never reload -- which is the one failure this cannot afford, since
// it is invisible and reads as the whole feature not working.
var bootID = newBootID()

func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Reached only if the system entropy source failed, at which point the
		// application has larger problems than a dev-time reload. A fixed value
		// degrades to "never reloads", never to "reloads at the wrong time".
		return "arandu"
	}
	return hex.EncodeToString(b[:])
}

// handleReload says which process is answering, and returns.
//
// It does NOT hold the connection. See the header of this file for why that
// mattered enough to be rewritten.
func (a *Application) handleReload(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	// Never cached, by anything. The whole answer is which process this is, and
	// a cached one is the previous process answering forever.
	h.Set("Cache-Control", "no-store, max-age=0")
	h.Set("Pragma", "no-cache")

	fmt.Fprintln(w, bootID)
}

// ReloadTagger is not declared here: it is hesape/foundation.ReloadTagger,
// aliased in module.go with the rest of the module vocabulary. What stays in
// this file is the endpoint it names and the middleware that injects what it
// returns.

// reloadTag is the markup injected into a document, filled at Boot from the
// module that brought it. Empty means nothing is injected, which is every
// arrangement outside development.
var reloadTag []byte

// liveReload injects the script into any full HTML document that leaves the
// application.
func liveReload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == reloadPath {
			next.ServeHTTP(w, r)
			return
		}
		rec := &htmlRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		rec.finish()
	})
}

// htmlRecorder buffers a response only for as long as it might be a document.
//
// A response that is not HTML is passed through on the first write, so a file
// download, a JSON payload and the event stream itself never sit in memory --
// which is what a buffering middleware gets wrong first.
type htmlRecorder struct {
	http.ResponseWriter
	// Zero until a status is chosen, which is what makes the content-type test
	// run: pre-seeding this with 200 meant WriteHeader was never reached, so
	// nothing was ever classified and an SVG containing a <body> element was
	// handed the script.
	status  int
	buf     bytes.Buffer
	passing bool // decided: not a document, writing straight through
	wrote   bool // the header has gone out
}

func (h *htmlRecorder) WriteHeader(status int) {
	h.status = status
	if !strings.HasPrefix(h.Header().Get("Content-Type"), "text/html") {
		h.passing = true
		h.wrote = true
		h.ResponseWriter.WriteHeader(status)
	}
}

func (h *htmlRecorder) Write(p []byte) (int, error) {
	if h.status == 0 {
		h.WriteHeader(http.StatusOK)
	}
	if h.passing {
		return h.ResponseWriter.Write(p)
	}
	return h.buf.Write(p)
}

// Flush is what makes an event stream work through this. Reaching it means the
// handler is streaming, so the response stops being a candidate for injection
// and whatever was buffered goes out as it is.
func (h *htmlRecorder) Flush() {
	if !h.passing {
		h.passing = true
		h.send(h.buf.Bytes())
	}
	if f, ok := h.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection through this
// wrapper, which is how a handler that holds a response open lifts the server's
// write deadline. Without it the deadline is liftable in production, where this
// middleware is not mounted, and not in development, where it is.
func (h *htmlRecorder) Unwrap() http.ResponseWriter { return h.ResponseWriter }

func (h *htmlRecorder) finish() {
	if h.passing {
		return
	}
	body := h.buf.Bytes()
	// The close tag is the test for a whole document. An HTMX fragment has no
	// </body>, and injecting into one would add an EventSource per swap.
	if at := bytes.LastIndex(body, []byte("</body>")); at >= 0 && len(reloadTag) > 0 {
		out := make([]byte, 0, len(body)+len(reloadTag))
		out = append(out, body[:at]...)
		out = append(out, reloadTag...)
		body = append(out, body[at:]...)
	}
	h.send(body)
}

func (h *htmlRecorder) send(body []byte) {
	if !h.wrote {
		h.wrote = true
		if h.status == 0 {
			h.status = http.StatusOK
		}
		// Rewritten rather than deleted: a wrong length is a truncated page, and
		// deleting it turns a sized response into a chunked one for no reason.
		if h.Header().Get("Content-Length") != "" {
			h.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		h.ResponseWriter.WriteHeader(h.status)
	}
	_, _ = h.ResponseWriter.Write(body)
}

// devReload is the middleware, or nothing outside development.
func devReload(isDev bool) []fhttp.Middleware {
	if !isDev {
		return nil
	}
	return []fhttp.Middleware{liveReload}
}
