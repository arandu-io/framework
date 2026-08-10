package kernel

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	// What Boot fills in from the module that brought it. Fixed here so these
	// tests are about the injection and not about the asset pipeline.
	reloadTag = []byte(`<script src="/_arandu/assets/abc/arandu-reload.js" defer></script>`)
	t.Cleanup(func() { reloadTag = nil })
	rec := httptest.NewRecorder()
	liveReload(handler).ServeHTTP(rec, req)
	return rec
}

// A whole document gets the script, once, inside the body.
func TestADocumentIsGivenTheReloadScript(t *testing.T) {
	rec := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Hello</h1></body></html>"))
	}, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if n := strings.Count(body, "arandu-reload.js"); n != 1 {
		t.Fatalf("the page carries %d listeners, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, "<h1>Hello</h1>") {
		t.Error("injecting the script lost the page")
	}
	if strings.Index(body, "arandu-reload.js") > strings.Index(body, "</body>") {
		t.Error("the script went outside the body")
	}
}

// The one that matters most. An HTMX swap replaces a fragment, and a fragment
// carrying the script would open one EventSource per interaction -- until the
// browser's six-per-origin limit is reached and the page stops working, which
// looks like anything except a dev-server feature.
func TestAnHTMXFragmentIsLeftAlone(t *testing.T) {
	rec := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<li id="comment-4">Said something</li>`))
	}, httptest.NewRequest(http.MethodGet, "/posts/1/comments", nil))

	if strings.Contains(rec.Body.String(), "arandu-reload.js") {
		t.Fatalf("a fragment was given a listener; every swap would add another:\n%s", rec.Body.String())
	}
	if rec.Body.String() != `<li id="comment-4">Said something</li>` {
		t.Error("the fragment was altered")
	}
}

// Anything that is not HTML is written straight through, never buffered.
func TestANonDocumentResponseIsUntouched(t *testing.T) {
	for _, tc := range []struct{ contentType, body string }{
		{"application/json", `{"ok":true}`},
		{"text/css", "body{color:red}"},
		{"image/svg+xml", "<svg><body></body></svg>"},
	} {
		rec := serve(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.contentType)
			_, _ = w.Write([]byte(tc.body))
		}, httptest.NewRequest(http.MethodGet, "/x", nil))

		if rec.Body.String() != tc.body {
			t.Errorf("%s was rewritten: %q", tc.contentType, rec.Body.String())
		}
	}
}

// A rewritten body needs a rewritten length, or the page is truncated at
// exactly the point the script was added.
func TestTheLengthFollowsTheBody(t *testing.T) {
	const page = "<html><body>x</body></html>"
	rec := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", strconv.Itoa(len(page)))
		_, _ = w.Write([]byte(page))
	}, httptest.NewRequest(http.MethodGet, "/", nil))

	declared, err := strconv.Atoi(rec.Header().Get("Content-Length"))
	if err != nil {
		t.Fatalf("no length on the response: %v", err)
	}
	if declared != rec.Body.Len() {
		t.Fatalf("declared %d bytes and wrote %d: the page is truncated where the script begins",
			declared, rec.Body.Len())
	}
}

// The status a handler chose survives the buffering.
func TestTheStatusIsPreserved(t *testing.T) {
	rec := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte("<html><body>no</body></html>"))
	}, httptest.NewRequest(http.MethodGet, "/auth/register", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: a rejected form would look accepted", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "arandu-reload.js") {
		t.Error("a 422 page does not follow the restart, and it is the one people iterate on")
	}
}

// A handler that flushes is streaming, and a streamed response must not be held
// until it ends -- which for an event stream is never.
func TestAStreamIsNotHeld(t *testing.T) {
	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		defer close(done)
		liveReload(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>first"))
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte("</body></html>"))
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-done

	if !strings.Contains(rec.Body.String(), "first") {
		t.Error("what was flushed never reached the client")
	}
	if strings.Contains(rec.Body.String(), "arandu-reload.js") {
		t.Error("a streaming response was rewritten, which means it was buffered first")
	}
}

// The stream itself says which process is answering, and says it immediately --
// a page that has to wait for a keepalive to learn the id would reload twenty
// seconds after the restart.
func TestTheStreamAnnouncesTheProcessAtOnce(t *testing.T) {
	k := &Kernel{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, reloadPath, nil)

	ctx, cancel := context.WithCancel(req.Context())
	cancel() // no keepalive loop; only what is written before the first select
	k.handleReload(rec, req.WithContext(ctx))

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q: the browser would not treat it as a stream", got)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("data: "+bootID)) {
		t.Fatalf("the stream does not identify the process:\n%s", rec.Body.String())
	}
}

// Two processes must not claim the same identity, or a page loaded by one never
// reloads when the other answers -- the single failure this feature cannot
// tolerate, because it is silent.
func TestEveryProcessHasItsOwnIdentity(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newBootID()
		if id == "arandu" {
			t.Fatal("the entropy source failed, so every process would claim one identity")
		}
		if seen[id] {
			t.Fatalf("two of a hundred boot ids collided (%s): a restart would go unnoticed", id)
		}
		seen[id] = true
	}
}

// Nothing is mounted or injected outside development.
func TestProductionHasNoneOfThis(t *testing.T) {
	if devReload(false) != nil {
		t.Fatal("the injection middleware is in the production pipeline")
	}
	if len(devReload(true)) != 1 {
		t.Fatal("development did not get the middleware")
	}
}

// TestShutdownDoesNotWaitForTheReloadStream.
//
// http.Server.Shutdown waits for every in-flight request, and the reload stream
// is deliberately in flight forever. So every restart of `aru dev` blocked for
// the whole shutdownTimeout with the port still held -- twenty seconds, during
// which the browser's clicks queued against a process that was already leaving
// and then all completed at once. It reads as the application hanging, and the
// only sign in the log is one line saying "context deadline exceeded" exactly
// shutdownTimeout after "shutdown started".
func TestShutdownDoesNotWaitForTheReloadStream(t *testing.T) {
	k := &Kernel{stopping: make(chan struct{})}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, reloadPath, nil)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		k.handleReload(rec, req)
	}()

	// It is holding the connection: nothing has returned, which is the whole
	// point of the endpoint.
	select {
	case <-returned:
		t.Fatal("the stream returned on its own, so it is not holding the connection at all")
	case <-time.After(50 * time.Millisecond):
	}

	if err := k.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream is still open after Shutdown, so Shutdown will wait the full shutdownTimeout for it")
	}
}

// Shutdown is called by Run and can be called again by a caller that does not
// know that. Closing the same channel twice panics.
func TestShutdownTwiceIsSafe(t *testing.T) {
	k := &Kernel{stopping: make(chan struct{})}
	if err := k.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := k.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

// A browser allows six connections per origin over HTTP/1.1 and a held stream
// spends one, so a reconnect delay that is too eager turns every dropped stream
// into a burst competing with the navigation the person is trying to do.
func TestTheReconnectDelayIsNotEager(t *testing.T) {
	if reconnectDelay < time.Second {
		t.Fatalf("the browser is told to reconnect after %v", reconnectDelay)
	}
	k := &Kernel{stopping: make(chan struct{})}
	close(k.stopping)

	rec := httptest.NewRecorder()
	k.handleReload(rec, httptest.NewRequest(http.MethodGet, reloadPath, nil))
	if !strings.Contains(rec.Body.String(), "retry: 1000") {
		t.Errorf("the stream does not tell the browser how long to wait:\n%s", rec.Body.String())
	}
}
