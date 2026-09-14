package foundation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fmiddleware "github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
)

// TestTheServerCarriesEveryLimit is the whole point of newServer being a
// function. Each of these fields is zero by default, zero means no limit, and a
// missing one produces no error and no log line -- it produces a server that
// holds a connection for as long as a client wants, which is only noticed when
// somebody makes it hold thousands.
func TestTheServerCarriesEveryLimit(t *testing.T) {
	a := New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: ":0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	})
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	srv := a.newServer(context.Background())
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout is unset: headers may arrive one byte at a time")
	}
	if srv.ReadTimeout == 0 {
		t.Error("ReadTimeout is unset: a body may arrive one byte at a time")
	}
	if srv.WriteTimeout == 0 {
		t.Error("WriteTimeout is unset: a client that never reads holds its goroutine")
	}
	if srv.IdleTimeout == 0 {
		t.Error("IdleTimeout is unset: an idle keep-alive connection is never closed")
	}
	if srv.MaxHeaderBytes == 0 {
		t.Error("MaxHeaderBytes is unset, so the limit is net/http's 1 MB rather than this framework's")
	}
}

// TestShutdownCancelsAStreamBeforeWaitingForIt pins the order that makes a
// graceful drain possible. net/http waits for active handlers during Shutdown,
// but deliberately does not cancel their request contexts. The Application owns
// the process lifecycle, so it must provide and cancel the base context first.
//
// The response has the shape of SSE and runs over a real connection: it writes
// and flushes one event, then holds the response until its one request context
// is cancelled. If cancellation moves below http.Server.Shutdown, this test
// expires its deliberately short drain context instead of passing.
func TestShutdownCancelsAStreamBeforeWaitingForIt(t *testing.T) {
	a := New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvDev,
			HTTPAddr: "127.0.0.1:0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	})
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	a.Use(fmiddleware.Observe(true, "", nil))

	started := make(chan struct{})
	deadlineLifted := make(chan error, 1)
	exited := make(chan error, 1)
	a.router.Get("/events", func(w http.ResponseWriter, r *http.Request) {
		deadlineLifted <- http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "event: ready\ndata: yes\n\n"); err != nil {
			exited <- fmt.Errorf("write first event: %w", err)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			exited <- fmt.Errorf("response writer does not implement http.Flusher")
			return
		}
		flusher.Flush()
		close(started)

		<-r.Context().Done()
		exited <- r.Context().Err()
	})

	srv := a.prepareServer(context.Background())
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(func() { client.CloseIdleConnections() })
	response, err := client.Get("http://" + listener.Addr().String() + "/events")
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("event stream did not flush its first event")
	}
	firstEvent := make([]byte, len("event: ready\ndata: yes\n\n"))
	if _, err := io.ReadFull(response.Body, firstEvent); err != nil {
		t.Fatalf("read first event: %v", err)
	}
	if got := string(firstEvent); !strings.Contains(got, "event: ready") {
		t.Fatalf("first event = %q", got)
	}
	if err := <-deadlineLifted; err != nil {
		t.Fatalf("lift write deadline through response wrappers: %v", err)
	}

	drainContext, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := a.shutdown(drainContext); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-exited:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream context ended with %v, want context.Canceled", err)
		}
	default:
		t.Fatal("Shutdown returned before the streaming handler exited")
	}

	if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve: %v, want http.ErrServerClosed", err)
	}
}

// TestWriteTimeoutOutlastsReadTimeout: the write deadline starts when the
// headers finished arriving and covers the body read too, so a write timeout
// below the read timeout would cut a slow upload on a write the handler had not
// reached -- the symptom being a handler that looks broken rather than a client
// that is slow.
func TestWriteTimeoutOutlastsReadTimeout(t *testing.T) {
	if writeTimeout <= readTimeout {
		t.Fatalf("writeTimeout = %s, readTimeout = %s: a slow upload fails on the write", writeTimeout, readTimeout)
	}
	if readTimeout <= readHeaderTimeout {
		t.Fatalf("readTimeout = %s, readHeaderTimeout = %s: the body gets no time of its own", readTimeout, readHeaderTimeout)
	}
}
