package feature

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/framework/test/servertrace"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/foundation"
	"github.com/arandu-io/hesape/log"
)

// execDriver is a database/sql driver that accepts every statement and says it
// changed one row. The statements reach it through database.DB, the handle
// every repository uses, which is what records them in the request's
// collector.
type execDriver struct{}

func (execDriver) Open(string) (driver.Conn, error) { return execConn{}, nil }

type execConn struct{}

func (execConn) Prepare(query string) (driver.Stmt, error) { return execStmt{query: query}, nil }
func (execConn) Close() error                              { return nil }
func (execConn) Begin() (driver.Tx, error)                 { return nil, errors.New("no transactions") }

type execStmt struct{ query string }

func (execStmt) Close() error  { return nil }
func (execStmt) NumInput() int { return -1 }
func (s execStmt) Exec([]driver.Value) (driver.Result, error) {
	if strings.Contains(s.query, "broken") {
		return nil, errors.New("no such table: broken")
	}
	return driver.RowsAffected(1), nil
}
func (execStmt) Query([]driver.Value) (driver.Rows, error) { return nil, errors.New("no queries") }

func init() { sql.Register("servertrace-exec", execDriver{}) }

// ownerPolicy lets the owner of an invoice view it.
type ownerPolicy struct{}

func (ownerPolicy) Can(_ context.Context, s auth.Subject, _ auth.Action, owner string) error {
	if s.ID != owner {
		return errors.New("not the owner")
	}
	return nil
}

// application is what an application's kernel builds: its logger at the top,
// Observe under it, and a handler that logs, writes a row and asks a policy.
func application(t *testing.T, dev bool, out io.Writer) http.Handler {
	t.Helper()
	handle, err := sql.Open("servertrace-exec", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	db := database.Wrap(handle, database.DialectSQLite)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		subject := auth.Subject{ID: r.URL.Query().Get("as"), Tenant: "tenant-a"}
		log.For(ctx).Info("viewing the invoice", "invoice", "inv-1")
		// auth.Authorize records the decision in the request's collector itself:
		// the handler does nothing for the trace to hold it.
		if _, err := auth.Authorize[string](ctx, ownerPolicy{}, subject, "invoice.view", "owner-1"); err != nil {
			log.For(ctx).Warn("refused", "error", err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if _, err := db.ExecContext(ctx, "UPDATE invoices SET viewed = ? WHERE id = ?", true, "inv-1"); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = db.ExecContext(ctx, "UPDATE broken SET x = 1")
		w.WriteHeader(http.StatusOK)
	})
	return log.Middleware(slog.New(slog.NewJSONHandler(out, nil)))(foundation.Observe(dev, "", nil)(handler))
}

// TestOneRequestCarriesItsLogsItsStatementsAndItsDecisions is the proof the
// trace needs: for one request, under the id its response carries, the lines
// it logged, the statements it ran and the Policy decision it was answered --
// with the application's own collector installed (development) and without it.
func TestOneRequestCarriesItsLogsItsStatementsAndItsDecisions(t *testing.T) {
	for _, dev := range []bool{true, false} {
		name := "outside development"
		if dev {
			name = "in development"
		}
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			trace := servertrace.New()
			server := httptest.NewServer(trace.Middleware(application(t, dev, &out)))
			defer server.Close()

			response, err := http.Get(server.URL + "/invoices/inv-1?as=owner-1")
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			id := response.Header.Get(servertrace.Header)
			if id == "" {
				t.Fatal("the response carries no request id")
			}
			section, ok := trace.Section(id)
			if !ok {
				t.Fatalf("no section under the id the response carries (%s); recorded %+v", id, trace.Sections())
			}
			if section.Method != http.MethodGet || section.Path != "/invoices/inv-1" || section.Status != http.StatusOK {
				t.Fatalf("section = %s %s %d", section.Method, section.Path, section.Status)
			}

			// The handler's line, and the access line Observe writes when the
			// request is over, in the order they were written.
			if len(section.Logs) != 2 || section.Logs[0].Message != "viewing the invoice" || section.Logs[0].Attrs["request_id"] != id || section.Logs[0].Attrs["invoice"] != "inv-1" {
				t.Fatalf("logs = %+v", section.Logs)
			}
			if access := section.Logs[1]; access.Message != "request completed" || access.Attrs["status"] != int64(http.StatusOK) {
				t.Fatalf("the access line = %+v", access)
			}
			if len(section.Queries) != 2 || section.Queries[0].SQL != "UPDATE invoices SET viewed = ? WHERE id = ?" || section.Queries[0].Args != 2 || section.Queries[0].Rows != 1 {
				t.Fatalf("queries = %+v", section.Queries)
			}
			if section.Queries[1].Error == "" || !strings.Contains(section.Queries[1].Error, "no such table: broken") {
				t.Fatalf("the failing statement carries no error: %+v", section.Queries[1])
			}
			if !strings.Contains(section.Queries[0].Caller, "servertrace_test.go") {
				t.Fatalf("the statement's caller is %q, want the handler", section.Queries[0].Caller)
			}
			if len(section.Decisions) != 1 {
				t.Fatalf("decisions = %+v", section.Decisions)
			}
			if d := section.Decisions[0]; d.Subject != "owner-1" || d.Tenant != "tenant-a" || d.Action != "invoice.view" || !d.Allowed || d.Reason != "" {
				t.Fatalf("decision = %+v", d)
			}
			if !strings.Contains(out.String(), `"request_id":"`+id+`"`) {
				t.Fatalf("the application's own log lines do not carry the id: %s", out.String())
			}
		})
	}
}

func TestADeniedDecisionIsRecordedWithItsReason(t *testing.T) {
	trace := servertrace.New()
	app := trace.Middleware(application(t, false, io.Discard))
	request := httptest.NewRequest(http.MethodGet, "/invoices/inv-1?as=mallory", nil)
	request.Header.Set(servertrace.Header, "abc-123")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)

	section, ok := trace.Section("abc-123")
	if !ok {
		t.Fatalf("the id the client sent was not kept; recorded %+v", trace.Sections())
	}
	if section.Status != http.StatusForbidden || len(section.Queries) != 0 {
		t.Fatalf("section = %+v", section)
	}
	if len(section.Decisions) != 1 || section.Decisions[0].Allowed || !strings.Contains(section.Decisions[0].Reason, "not the owner") {
		t.Fatalf("decisions = %+v", section.Decisions)
	}
	if len(section.Logs) != 3 || section.Logs[1].Level != "WARN" || section.Logs[1].Message != "refused" {
		t.Fatalf("logs = %+v", section.Logs)
	}
}

func TestAnIDTheApplicationWouldRefuseIsReplacedBeforeItArrives(t *testing.T) {
	trace := servertrace.New()
	app := trace.Middleware(application(t, true, io.Discard))
	request := httptest.NewRequest(http.MethodGet, "/invoices/inv-1?as=owner-1", nil)
	request.Header.Set(servertrace.Header, "not hex\nforged")
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)

	id := recorder.Header().Get(servertrace.Header)
	if id == "" || strings.Contains(id, "forged") {
		t.Fatalf("the response carries %q", id)
	}
	if _, ok := trace.Section(id); !ok {
		t.Fatalf("no section under %q; recorded %+v", id, trace.Sections())
	}
	if request.Header.Get(servertrace.Header) != "not hex\nforged" {
		t.Fatal("the middleware changed the caller's request")
	}
}

func TestConcurrentRequestsKeepTheirOwnSections(t *testing.T) {
	trace := servertrace.New()
	server := httptest.NewServer(trace.Middleware(application(t, true, io.Discard)))
	defer server.Close()

	var wg sync.WaitGroup
	ids := make([]string, 20)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			who := "owner-1"
			if i%2 == 1 {
				who = "mallory"
			}
			response, err := http.Get(server.URL + "/invoices/inv-1?as=" + who)
			if err != nil {
				t.Error(err)
				return
			}
			_ = response.Body.Close()
			ids[i] = response.Header.Get(servertrace.Header)
		}()
	}
	wg.Wait()

	if got := len(trace.Sections()); got != len(ids) {
		t.Fatalf("recorded %d sections, want %d", got, len(ids))
	}
	for i, id := range ids {
		section, ok := trace.Section(id)
		if !ok || len(section.Decisions) != 1 {
			t.Fatalf("request %d: section %+v", i, section)
		}
		if allowed := i%2 == 0; section.Decisions[0].Allowed != allowed {
			t.Fatalf("request %d holds another request's decision: %+v", i, section.Decisions[0])
		}
	}
}

func TestASectionIsJSONTheBrowserTraceCanEmbed(t *testing.T) {
	trace := servertrace.New()
	app := trace.Middleware(application(t, false, io.Discard))
	request := httptest.NewRequest(http.MethodGet, "/invoices/inv-1?as=owner-1", nil)
	request.Header.Set(servertrace.Header, "feed")
	app.ServeHTTP(httptest.NewRecorder(), request)

	section, _ := trace.Section("feed")
	encoded, err := json.Marshal(section)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"request_id":"feed"`, `"logs":[`, `"queries":[`, `"decisions":[`, `"allowed":true`} {
		if !bytes.Contains(encoded, []byte(key)) {
			t.Errorf("the section JSON lacks %s: %s", key, encoded)
		}
	}
	if bytes.Contains(encoded, []byte("inv-1\"]")) {
		t.Errorf("the statement's bound values reached the trace: %s", encoded)
	}
}
