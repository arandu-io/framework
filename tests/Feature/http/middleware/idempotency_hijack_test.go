package feature

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/hesape/cache"
)

// A write whose handler took the connection over left no answer to keep, so a
// retry with the same key runs again instead of being replayed an empty 200.
//
// The recorder only sees what goes through the writer. A takeover through
// http.ResponseController went around it by Unwrap, the recorder saw no status,
// and the empty 200 net/http would have sent was stored as the answer --
// replayed to every retry for as long as the key lives, to a client that had
// asked to switch protocols.
func TestAHijackedWriteKeepsNoAnswerToReplay(t *testing.T) {
	tokens := newIssuedTokens()
	tokens.issue("tok-alice", alice)

	var runs atomic.Int32
	r := fhttp.NewRouter()
	r.Post("/tunnel", func(w http.ResponseWriter, req *http.Request) {
		runs.Add(1)
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tunnel\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
	}, middleware.RequireToken(tokens), middleware.Idempotent(cache.NewArrayStore(), time.Hour))

	done := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.ServeHTTP(w, req)
		done <- struct{}{}
	}))
	defer srv.Close()

	open := func() string {
		t.Helper()
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.WriteString(conn, "POST /tunnel HTTP/1.1\r\nHost: example.test\r\n"+
			"Authorization: Bearer tok-alice\r\nIdempotency-Key: key-1\r\nContent-Length: 0\r\n"+
			"Connection: Upgrade\r\nUpgrade: tunnel\r\n\r\n")
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			t.Fatalf("reading the status line: %v", err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the request never finished")
		}
		return strings.TrimSpace(line)
	}

	first, retry := open(), open()
	if first != "HTTP/1.1 101 Switching Protocols" {
		t.Fatalf("the first request read %q", first)
	}
	if retry != first || runs.Load() != 2 {
		t.Errorf("the retry read %q after %d runs, want a second run answering %q", retry, runs.Load(), first)
	}
}

// 101 is a final status, as net/http reads it, and it is not an answer a retry
// can be given: it switched one connection to another protocol. So a write
// answered 101 keeps nothing, and the retry runs again.
//
// The recorder used to take 101 for an informational status and wait for the
// final one. The handler's next write then recorded an implicit 200, which was
// kept and replayed to every retry -- a 200 with an empty body, to a client
// whose first attempt had been told to switch protocols.
func TestASwitchingProtocolsAnswerIsNotKeptForReplay(t *testing.T) {
	tokens := newIssuedTokens()
	tokens.issue("tok-alice", alice)

	var runs atomic.Int32
	r := fhttp.NewRouter()
	r.Post("/switch", func(w http.ResponseWriter, req *http.Request) {
		runs.Add(1)
		w.Header().Set("Upgrade", "tunnel")
		w.Header().Set("Connection", "Upgrade")
		w.WriteHeader(http.StatusSwitchingProtocols)
		// net/http refuses a body after 101; the write is what used to make
		// the recorder take an implicit 200.
		_, _ = w.Write([]byte("x"))
	}, middleware.RequireToken(tokens), middleware.Idempotent(cache.NewArrayStore(), time.Hour))

	done := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.ServeHTTP(w, req)
		done <- struct{}{}
	}))
	defer srv.Close()

	open := func() string {
		t.Helper()
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "POST /switch HTTP/1.1\r\nHost: example.test\r\n"+
			"Authorization: Bearer tok-alice\r\nIdempotency-Key: key-101\r\nContent-Length: 0\r\n"+
			"Connection: Upgrade\r\nUpgrade: tunnel\r\n\r\n")
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			t.Fatalf("reading the status line: %v", err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the request never finished")
		}
		return strings.TrimSpace(line)
	}

	first, retry := open(), open()
	if first != "HTTP/1.1 101 Switching Protocols" {
		t.Fatalf("the first request read %q", first)
	}
	if retry != first || runs.Load() != 2 {
		t.Errorf("the retry read %q after %d runs, want a second run answering %q", retry, runs.Load(), first)
	}
}

// Early Hints ahead of the answer are not the answer: what is kept and replayed
// is the final status and its body.
func TestEarlyHintsAreNotKeptAsTheAnswer(t *testing.T) {
	tokens := newIssuedTokens()
	tokens.issue("tok-alice", alice)

	var runs atomic.Int32
	r := fhttp.NewRouter()
	r.Post("/hinted", func(w http.ResponseWriter, req *http.Request) {
		runs.Add(1)
		w.Header().Set("Link", "</app.css>; rel=preload; as=style")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "made")
	}, middleware.RequireToken(tokens), middleware.Idempotent(cache.NewArrayStore(), time.Hour))
	srv := httptest.NewServer(r)
	defer srv.Close()

	post := func() (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hinted", nil)
		req.Header.Set("Authorization", "Bearer tok-alice")
		req.Header.Set("Idempotency-Key", "key-103")
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}

	for i := range 2 {
		if status, body := post(); status != http.StatusCreated || body != "made" {
			t.Errorf("attempt %d received %d %q, want 201 \"made\"", i+1, status, body)
		}
	}
	if runs.Load() != 1 {
		t.Errorf("the handler ran %d times, want the retry replayed", runs.Load())
	}
}
