package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/observability"
)

// TestTheOutboundCallReachesTheTimeline: without this, "external" is always zero
// and the console says nothing about the API the handler waited on -- which is
// the wrong answer for the request whose 800ms were spent in somebody else's
// service.
func TestTheOutboundCallReachesTheTimeline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()

	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	client := &http.Client{Transport: observability.Transport(nil)}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/rates", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if len(col.External()) != 1 {
		t.Fatalf("%d outbound calls recorded, want 1", len(col.External()))
	}
	call := col.External()[0]
	if call.Method != http.MethodGet || call.Status != http.StatusTeapot {
		t.Errorf("recorded %+v", call)
	}
	if !strings.HasSuffix(call.URL, "/rates") {
		t.Errorf("URL = %q", call.URL)
	}
	if call.Duration <= 0 {
		t.Error("the call took no time at all")
	}
}

// TestTheQueryStringIsNotRecorded: a URL can carry a token, and the console is a
// page people screenshot.
func TestTheQueryStringIsNotRecorded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	client := &http.Client{Transport: observability.Transport(nil)}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		upstream.URL+"/rates?api_key=s3cret&from=BRL", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if got := col.External()[0].URL; strings.Contains(got, "s3cret") || strings.Contains(got, "?") {
		t.Errorf("the query string reached the console: %q", got)
	}
}

// TestAFailedCallIsStillRecorded: the call that never connected is the one worth
// seeing on the timeline.
func TestAFailedCallIsStillRecorded(t *testing.T) {
	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	client := &http.Client{Transport: observability.Transport(nil), Timeout: 300 * time.Millisecond}
	// Port 1 refuses connections everywhere.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/rates", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("connecting to nothing succeeded")
	}

	if len(col.External()) != 1 {
		t.Fatalf("a failed call was not recorded")
	}
	if col.External()[0].Status != 0 {
		t.Errorf("status = %d, want 0 for a call that never got a response", col.External()[0].Status)
	}
}

// TestRecordingAnOutboundCallCostsNothingInProduction: the transport is wired
// once, in main, and then every call in the application goes through it.
func TestRecordingAnOutboundCallCostsNothingInProduction(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	client := &http.Client{Transport: observability.Transport(nil)}
	// No Collector in the context, which is production.
	req, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
}

func TestClientHasATimeout(t *testing.T) {
	if observability.Client(5*time.Second).Timeout != 5*time.Second {
		t.Fatal("the timeout was not applied")
	}
}
