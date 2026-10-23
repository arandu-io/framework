package trace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriteThenReadKeepsEverySection(t *testing.T) {
	r := NewRecorder("TestCheckout/pays with a card")
	r.SetDriver("cdp", "chromium")
	step := r.Step(Step{Action: "click role=button name=Pay", URL: "http://127.0.0.1/checkout"}, []byte("\x89PNG"), []byte("<html></html>"))
	if step.Index != 1 || step.Screenshot != "steps/001-click-role-button-name-Pay.png" || step.DOM != "steps/001-click-role-button-name-Pay.html" {
		t.Fatalf("step = %+v", step)
	}
	r.Request(Request{ID: "1", Method: "POST", URL: "http://127.0.0.1/pay", Started: time.Now()})
	r.Request(Request{ID: "1", Status: 500, ServerRequestID: "req-7"})
	r.Console(Message{Level: "error", Source: "console-api", Text: "boom"})
	r.Fail(Failure{Kind: "server-error", Text: "POST /pay answered 500"})
	r.Server("req-7", Server{Logs: []LogLine{{Level: "ERROR", Message: "card refused"}}})
	r.Server("req-7", Server{Queries: []Query{{SQL: "select 1"}}, Decisions: []Decision{{Policy: "InvoicePolicy", Action: "pay", Allowed: true}}})
	r.Server("", Server{Logs: []LogLine{{Message: "dropped"}}})

	dir := filepath.Join(t.TempDir(), "trace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.png"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Write(dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.png")); !os.IsNotExist(err) {
		t.Fatalf("a file from an earlier run survived the write: %v", err)
	}
	png, err := os.ReadFile(filepath.Join(dir, "steps", "001-click-role-button-name-Pay.png"))
	if err != nil || !bytes.Equal(png, []byte("\x89PNG")) {
		t.Fatalf("screenshot = %q, %v", png, err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Failed || got.Driver != "cdp" || got.Browser != "chromium" || got.Test != "TestCheckout/pays with a card" {
		t.Fatalf("header = %+v", got)
	}
	if len(got.Network) != 1 || got.Network[0].Status != 500 || got.Network[0].Method != "POST" || got.Network[0].ServerRequestID != "req-7" {
		t.Fatalf("the response did not complete the request it answered: %+v", got.Network)
	}
	if len(got.Console) != 1 || len(got.Failures) != 1 || len(got.Steps) != 1 {
		t.Fatalf("sections = %d console, %d failures, %d steps", len(got.Console), len(got.Failures), len(got.Steps))
	}
	server := got.Server["req-7"]
	if server == nil || len(server.Logs) != 1 || len(server.Queries) != 1 || len(server.Decisions) != 1 || len(got.Server) != 1 {
		t.Fatalf("server section = %+v", got.Server)
	}
}

func TestReadRefusesAnotherVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if err == nil || !strings.Contains(err.Error(), "format version 99") {
		t.Fatalf("err = %v", err)
	}
}

func TestDirIsOneDirectoryPerTest(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE_DIR", "/traces")
	got := Dir("TestInvoices/opens the modal/../..")
	want := filepath.Join("/traces", "TestInvoices", "opens-the-modal", "step", "step")
	if got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

func TestAlwaysReadsTheEnvironment(t *testing.T) {
	t.Setenv("ARANDU_E2E_TRACE", "")
	if Always() {
		t.Fatal("Always with ARANDU_E2E_TRACE unset")
	}
	t.Setenv("ARANDU_E2E_TRACE", "1")
	if !Always() {
		t.Fatal("Always ignored ARANDU_E2E_TRACE=1")
	}
}

func TestTheRecorderIsSafeForConcurrentEvents(t *testing.T) {
	r := NewRecorder("TestRace")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Console(Message{Text: "line"})
			r.Request(Request{ID: string(rune('a' + i))})
			r.Server("x", Server{Logs: []LogLine{{Message: "m"}}})
		}()
	}
	r.Step(Step{Action: "visit"}, nil, nil)
	wg.Wait()
	s := r.Snapshot()
	if len(s.Console) != 8 || len(s.Network) != 8 || len(s.Server["x"].Logs) != 8 {
		t.Fatalf("snapshot = %d console, %d network, %d logs", len(s.Console), len(s.Network), len(s.Server["x"].Logs))
	}
}
