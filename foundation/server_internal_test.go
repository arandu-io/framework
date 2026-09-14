package foundation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
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
	if srv.Protocols == nil {
		t.Fatal("Protocols is unset: the application server cannot declare its TLS protocol boundary")
	}
	if !srv.Protocols.HTTP1() {
		t.Error("HTTP/1 is disabled: existing REST clients cannot connect")
	}
	if !srv.Protocols.HTTP2() {
		t.Error("HTTP/2 over TLS is disabled")
	}
	if srv.Protocols.UnencryptedHTTP2() {
		t.Error("unencrypted HTTP/2 is enabled on the application listener")
	}
}

func TestOneTLSListenerServesRESTOverHTTP1AndRPCStreamsOverHTTP2(t *testing.T) {
	a := New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: "127.0.0.1:0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	})
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	a.router.Get("/rest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "rest:%d", r.ProtoMajor)
	})
	started := make(chan int, 1)
	exited := make(chan error, 1)
	a.router.Post("/rpc.example.Service/{procedure}", func(w http.ResponseWriter, r *http.Request) {
		started <- r.ProtoMajor
		if _, err := io.WriteString(w, "ready"); err != nil {
			exited <- fmt.Errorf("write first message: %w", err)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			exited <- errors.New("response writer does not implement http.Flusher")
			return
		}
		flusher.Flush()
		<-r.Context().Done()
		exited <- r.Context().Err()
	})

	requestContext, cancelRequests := context.WithCancel(context.Background())
	tlsServer := httptest.NewUnstartedServer(nil)
	tlsServer.Config = a.newServer(requestContext)
	tlsServer.EnableHTTP2 = true
	tlsServer.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	tlsServer.StartTLS()
	t.Cleanup(tlsServer.Close)

	a.serverMu.Lock()
	a.srv = tlsServer.Config
	a.cancelRequests = cancelRequests
	a.serverMu.Unlock()

	http1Protocols := new(http.Protocols)
	http1Protocols.SetHTTP1(true)
	http1Transport := tlsServer.Client().Transport.(*http.Transport).Clone()
	http1Transport.Protocols = http1Protocols
	http1Transport.ForceAttemptHTTP2 = false
	http1Transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	http1Client := &http.Client{Transport: http1Transport}
	t.Cleanup(http1Transport.CloseIdleConnections)
	assertResponse(t, http1Client, tlsServer.URL+"/rest", 1, "rest:1")

	http2Protocols := new(http.Protocols)
	http2Protocols.SetHTTP2(true)
	http2Transport := tlsServer.Client().Transport.(*http.Transport).Clone()
	http2Transport.Protocols = http2Protocols
	http2Client := &http.Client{Transport: http2Transport}
	t.Cleanup(http2Transport.CloseIdleConnections)
	response, err := http2Client.Post(tlsServer.URL+"/rpc.example.Service/Stream", "application/proto", nil)
	if err != nil {
		t.Fatalf("open RPC stream: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.ProtoMajor != 2 {
		t.Fatalf("mounted stream used HTTP/%d, want HTTP/2", response.ProtoMajor)
	}
	firstMessage := make([]byte, len("ready"))
	if _, err := io.ReadFull(response.Body, firstMessage); err != nil {
		t.Fatalf("read first message: %v", err)
	}
	if got := string(firstMessage); got != "ready" {
		t.Fatalf("first message = %q, want ready", got)
	}
	if got := <-started; got != 2 {
		t.Fatalf("mounted handler received HTTP/%d, want HTTP/2", got)
	}

	drainContext, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
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
		t.Fatal("Shutdown returned before the RPC stream exited")
	}
}

func TestRunNegotiatesHTTP2WithTheConfiguredTLSPair(t *testing.T) {
	certificateFile, keyFile, roots := writeServerCertificate(t)

	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listener address: %v", err)
	}
	address := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatalf("release listener address: %v", err)
	}

	a := New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: address,
			Key:      make([]byte, encryption.KeySize),
		},
		HTTP: bootstrap.HTTPServer{
			TLSCertFile: certificateFile,
			TLSKeyFile:  keyFile,
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	})
	a.router.Get("/protocol", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "http/%d", r.ProtoMajor)
	})
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(stop)

	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
		},
		Protocols: protocols,
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}

	url := "https://" + address + "/protocol"
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, requestErr := client.Get(url)
		if requestErr == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatalf("read response: %v", readErr)
			}
			if response.ProtoMajor != 2 {
				t.Fatalf("Run negotiated HTTP/%d, want HTTP/2", response.ProtoMajor)
			}
			if got := string(body); got != "http/2" {
				t.Fatalf("handler saw %q, want http/2", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connect to Run TLS listener: %v", requestErr)
		}
		time.Sleep(5 * time.Millisecond)
	}

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after its context was cancelled")
	}
}

func writeServerCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "arandu runtime test"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}

	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "server.crt")
	keyFile := filepath.Join(directory, "server.key")
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return certificateFile, keyFile, roots
}

func assertResponse(t *testing.T, client *http.Client, url string, protocolMajor int, body string) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if response.ProtoMajor != protocolMajor {
		t.Fatalf("GET %s used HTTP/%d, want HTTP/%d", url, response.ProtoMajor, protocolMajor)
	}
	if string(got) != body {
		t.Fatalf("GET %s body = %q, want %q", url, got, body)
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

type shutdownOrderModule struct {
	events *[]string
}

func (*shutdownOrderModule) Name() string { return "rpc" }

func (*shutdownOrderModule) Routes(*fhttp.Router) {}

func (m *shutdownOrderModule) CloseAdmission() {
	*m.events = append(*m.events, "admission")
}

func (m *shutdownOrderModule) Close(context.Context) error {
	*m.events = append(*m.events, "close")
	return nil
}

func TestShutdownClosesAdmissionBeforeCancellingRequestsAndClosingModules(t *testing.T) {
	var events []string
	a := New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: "127.0.0.1:0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	}).Register(&shutdownOrderModule{events: &events})
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	a.cancelRequests = func() { events = append(events, "cancel") }

	if err := a.shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	want := []string{"admission", "cancel", "close"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("shutdown order = %v, want %v", events, want)
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
