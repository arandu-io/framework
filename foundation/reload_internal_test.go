package foundation

import (
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
	reloadTag := []byte(`<script src="/_arandu/assets/abc/arandu-reload.js" defer></script>`)
	rec := httptest.NewRecorder()
	liveReload(reloadTag, handler).ServeHTTP(rec, req)
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
		liveReload([]byte(`<script src="/_arandu/assets/abc/arandu-reload.js" defer></script>`), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// A handler that holds a response open lifts the server's write deadline with
// http.ResponseController, and the controller finds the connection by unwrapping
// the writers it was handed. This middleware is mounted in development and
// absent from production, so a wrapper without Unwrap is a deadline that can be
// lifted in production and not on the machine of the person writing the handler
// -- the one place the difference is invisible until it is deployed.
//
// It goes over a real connection because httptest.ResponseRecorder implements no
// deadline at all, so a recorder would refuse the call whether the wrapper
// unwraps or not.
func TestAStreamingHandlerCanLiftTheWriteDeadlineThroughTheWrapper(t *testing.T) {
	errc := make(chan error, 1)
	srv := httptest.NewServer(liveReload(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errc <- http.NewResponseController(w).SetWriteDeadline(time.Time{})
	})))
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = res.Body.Close()

	if err := <-errc; err != nil {
		t.Fatalf("SetWriteDeadline: %v -- a streaming handler cannot lift the server's write timeout in development", err)
	}
}

// Nothing is mounted or injected outside development.
func TestProductionHasNoneOfThis(t *testing.T) {
	if devReload(false, nil) != nil {
		t.Fatal("the injection middleware is in the production pipeline")
	}
	if len(devReload(true, nil)) != 1 {
		t.Fatal("development did not get the middleware")
	}
}

// TestAskingWhichProcessAnsweredDoesNotHoldTheConnection.
//
// This is the whole reason the endpoint stopped being an EventSource. A browser
// allows six connections per origin over HTTP/1.1 and a held stream spends one
// -- and on navigation the browser abandons the stream of the page it is leaving
// without closing it, so the server keeps the slot until its next write fails.
//
// Six quick navigations therefore left six dead streams holding every slot, and
// the seventh click did nothing at all. Reported as "I navigate, I go back, I
// navigate, and it freezes", and that is exactly what it was. Shutdown waiting
// its full timeout for the same request was the second symptom of the one
// defect.
func TestAskingWhichProcessAnsweredDoesNotHoldTheConnection(t *testing.T) {
	k := &Application{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		k.handleReload(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, reloadPath, nil))
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the handler is holding the connection, which is what froze the browser after a handful of clicks")
	}
}

// The answer names this process and nothing else, and no cache may keep it: a
// cached answer is the previous process answering forever, so the page would
// never learn that the code changed.
func TestTheAnswerIsThisProcessAndIsNeverCached(t *testing.T) {
	k := &Application{}
	rec := httptest.NewRecorder()
	k.handleReload(rec, httptest.NewRequest(http.MethodGet, reloadPath, nil))

	if got := strings.TrimSpace(rec.Body.String()); got != bootID {
		t.Fatalf("the answer is %q, want the boot id %q", got, bootID)
	}
	if store := rec.Header().Get("Cache-Control"); !strings.Contains(store, "no-store") {
		t.Errorf("Cache-Control is %q: a cached answer is the old process answering forever", store)
	}
}
