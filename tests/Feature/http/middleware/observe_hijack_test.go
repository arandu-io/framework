package feature

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/observability"
)

// observed serves h behind Observe on a real server, the way an application
// mounts it, and hands back each access line as it is written.
//
// The line is written after the handler returns, which for a takeover is after
// the client already has its answer, so the test waits for the request to
// finish rather than for the response.
func observed(t *testing.T, recorder *observability.Recorder, h http.Handler) (*httptest.Server, func() map[string]any) {
	t.Helper()

	var (
		mu   sync.Mutex
		buf  bytes.Buffer
		done = make(chan struct{}, 4)
	)
	logger := slog.New(slog.NewJSONHandler(lockedWriter{&mu, &buf}, nil))
	chain := fhttp.Chain(h, middleware.Observe(recorder != nil, "", recorder))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chain.ServeHTTP(w, r.WithContext(observability.WithLogger(r.Context(), logger)))
		done <- struct{}{}
	}))
	t.Cleanup(srv.Close)

	next := func() map[string]any {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the request never finished")
		}
		mu.Lock()
		defer mu.Unlock()
		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("decoding %q: %v", line, err)
			}
			if entry["msg"] == "request completed" {
				buf.Reset()
				return entry
			}
		}
		t.Fatalf("no access line was written:\n%s", buf.String())
		return nil
	}
	return srv, next
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// switchProtocols takes the connection the way a websocket server does --
// through a ResponseController, which asks the outermost writer for Hijack
// before it unwraps -- and answers 101 on the raw connection, where no wrapper
// sees it.
func switchProtocols(hold time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
		time.Sleep(hold)
	}
}

func dialUpgrade(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: example.test\r\n"+
		"Connection: keep-alive, Upgrade\r\nUpgrade: websocket\r\n\r\n")
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the status line: %v", err)
	}
	return strings.TrimSpace(line)
}

// A websocket upgrade is logged as the 101 it answered, not as the 200 the
// wrapper was seeded with, and its time is the connection's lifetime.
//
// The server writes the 101 on the raw connection, so the wrapper never sees a
// status. Before Hijack was a method of its own, the controller went around the
// wrapper through Unwrap and the access log said status=200 with the duration
// of the whole connection, which reads as one very slow page.
func TestAnUpgradedConnectionIsLoggedAsSwitchingProtocols(t *testing.T) {
	srv, next := observed(t, nil, switchProtocols(20*time.Millisecond))

	if got := dialUpgrade(t, srv, "/app/key"); got != "HTTP/1.1 101 Switching Protocols" {
		t.Fatalf("the client read %q", got)
	}

	entry := next()
	if entry["status"] != float64(http.StatusSwitchingProtocols) {
		t.Errorf("status = %v, want 101; line: %v", entry["status"], entry)
	}
	if entry["hijacked"] != true {
		t.Errorf("the line does not say the connection was taken over: %v", entry)
	}
	if entry["upgrade"] != "websocket" {
		t.Errorf("upgrade = %v, want websocket", entry["upgrade"])
	}
	if _, ok := entry["connection_ms"]; !ok {
		t.Errorf("the lifetime is not labeled as the connection's: %v", entry)
	}
	for _, key := range []string{"duration_ms", "bytes"} {
		if _, ok := entry[key]; ok {
			t.Errorf("%s is on a taken-over connection, where it measures nothing the writer carried: %v", key, entry)
		}
	}
}

// A takeover that was not an upgrade has no status to log, and the line says
// what happened instead of inventing one.
func TestAHijackThatIsNotAnUpgradeIsMarkedWithoutAStatus(t *testing.T) {
	srv, next := observed(t, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))

	res, err := srv.Client().Get(srv.URL + "/raw")
	if err == nil {
		_ = res.Body.Close()
	}

	entry := next()
	if entry["hijacked"] != true {
		t.Errorf("the line does not say the connection was taken over: %v", entry)
	}
	if status, ok := entry["status"]; ok {
		t.Errorf("status = %v on a connection that answered nothing through the writer", status)
	}
}

// An ordinary request still logs the status it answered.
func TestARequestThatIsNotTakenOverLogsItsStatus(t *testing.T) {
	srv, next := observed(t, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "short and stout")
	}))

	res, err := srv.Client().Get(srv.URL + "/pot")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = res.Body.Close()

	entry := next()
	if entry["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want 418", entry["status"])
	}
	if entry["bytes"] != float64(len("short and stout")) {
		t.Errorf("bytes = %v", entry["bytes"])
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Errorf("an answered request lost its duration: %v", entry)
	}
	if _, ok := entry["hijacked"]; ok {
		t.Errorf("an answered request is marked hijacked: %v", entry)
	}
}

// The console's record carries the 101 too, and the Collector says the
// connection was taken.
func TestTheConsoleRecordsAnUpgradeAsSwitchingProtocols(t *testing.T) {
	recorder := observability.NewRecorder(4)
	srv, next := observed(t, recorder, switchProtocols(0))

	_ = dialUpgrade(t, srv, "/app/key")
	_ = next()

	recent := recorder.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(recent))
	}
	if recent[0].Status != http.StatusSwitchingProtocols {
		t.Errorf("the console records status %d, want 101", recent[0].Status)
	}
	var marked bool
	for _, event := range recent[0].Collector.Events() {
		marked = marked || event.Name == "http.hijacked"
	}
	if !marked {
		t.Errorf("the Collector does not say the connection was taken: %v", recent[0].Collector.Events())
	}
}

// Wrapping Hijack must not cost the wrapper what it already passed through: a
// streaming handler still flushes and still lifts the write deadline.
func TestFlushAndDeadlinesStillReachTheConnectionThroughObserve(t *testing.T) {
	errc := make(chan error, 2)
	srv, next := observed(t, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		errc <- rc.SetWriteDeadline(time.Time{})
		_, _ = io.WriteString(w, "data: ping\n\n")
		errc <- rc.Flush()
	}))

	res, err := srv.Client().Get(srv.URL + "/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = res.Body.Close()
	entry := next()

	for range 2 {
		if err := <-errc; err != nil {
			t.Errorf("through Observe: %v", err)
		}
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", entry["status"])
	}
}

// A takeover the writer underneath refused is not one: the handler answers
// through the writer, and that answer is what the line reports.
//
// httptest.ResponseRecorder cannot be hijacked, which is the same refusal an
// HTTP/2 stream gives.
func TestARefusedHijackLogsTheAnswerTheHandlerGave(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	chain := fhttp.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
			http.Error(w, "this connection cannot be upgraded", http.StatusInternalServerError)
		}
	}), middleware.Observe(false, "", nil))

	r := httptest.NewRequest(http.MethodGet, "/app/key", nil)
	chain.ServeHTTP(httptest.NewRecorder(), r.WithContext(observability.WithLogger(r.Context(), logger)))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("decoding %q: %v", buf.String(), err)
	}
	if entry["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("status = %v, want 500", entry["status"])
	}
	if _, ok := entry["hijacked"]; ok {
		t.Errorf("a refused takeover is marked hijacked: %v", entry)
	}
}
