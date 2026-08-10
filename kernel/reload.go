package kernel

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/arandu-io/framework/httpx"
)

// Live reload: the browser follows the restart, in development only.
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
// The CSP is script-src 'self' (RULE 13), and an inline <script> is refused by
// it -- the browser would drop the tag silently and the feature would appear
// simply not to work. So the script is registered as an ordinary asset and
// referenced by src, content-addressed like every other one. It costs one
// request, on a development server, once per page.
//
// # What is deliberately not here
//
// No CSS hot-swap, no state preservation, no module replacement. Those need a
// client-side runtime and a module graph, which is the JavaScript build RULE 13
// exists to refuse. A full reload against a Go server that restarts in under a
// second is a fair trade, and it is one behaviour rather than two.

// reloadPath is where the browser listens. Under _arandu with health and the
// console, so one prefix covers everything the framework mounts.
const reloadPath = "/_arandu/reload"

// reconnectDelay is how long the browser waits before listening again.
//
// A second, not the 250ms this shipped with. HTTP/1.1 allows a browser six
// connections per origin and a held stream spends one of them, so a reconnect
// that is too eager turns every dropped stream into a burst that competes with
// the navigation the person is trying to do.
const reconnectDelay = time.Second

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

// handleReload holds the connection open and says which process is answering.
func (k *Kernel) handleReload(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Told explicitly, because a proxy that buffers this buffers the one byte
	// the page is waiting for and the reload never arrives.
	h.Set("X-Accel-Buffering", "no")

	fmt.Fprintf(w, "retry: %d\ndata: %s\n\n", reconnectDelay.Milliseconds(), bootID)
	flusher.Flush()

	// A comment every twenty seconds. It carries nothing; it exists so that an
	// idle connection is not closed by a proxy or by the browser, which would
	// make the page reload on a reconnect that nothing caused.
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		// The server is going down. Without this the connection is one Shutdown
		// waits the whole shutdownTimeout for, holding the port -- twenty seconds
		// per restart of `aru dev`, which reads as the application hanging.
		case <-k.stopping:
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ReloadTagger is what a module implements to supply the development live-reload
// tag.
//
// Optional, and asked for the same way the renderer is: the kernel cannot import
// the view package -- that package imports this one in order to be a Module --
// so what it needs arrives through an interface declared here and satisfied
// there. The kernel supplies the address of its own stream, because the route is
// its own, and two constants for one address is how a client and a server come
// to disagree about it.
type ReloadTagger interface {
	ReloadTag(stream string) string
}

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
func devReload(isDev bool) []httpx.Middleware {
	if !isDev {
		return nil
	}
	return []httpx.Middleware{liveReload}
}
