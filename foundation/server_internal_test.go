package foundation

import (
	"context"
	"log/slog"
	"testing"

	"github.com/arandu-io/framework/foundation/bootstrap"
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

	srv := a.newServer()
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
