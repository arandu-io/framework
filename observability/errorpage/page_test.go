package errorpage_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/observability/errorpage"
)

func TestRenderShowsQueriesDumpsAndEvents(t *testing.T) {
	col := observability.NewCollector("req-abc")
	col.RecordQuery(`SELECT * FROM invoices WHERE id = $1`, []any{"inv-1"}, 3*time.Millisecond, 1, nil)
	col.RecordEvent("invoice.viewed", "inv-1")
	col.RecordExternal("POST", "https://payments.test/charge", 502, 900*time.Millisecond)
	observability.Dump(observability.WithCollector(t.Context(), col), "invoice", map[string]string{"number": "42"})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/invoices/inv-1", nil)

	errorpage.Render(rec, r, errors.New("boom"), col, errorpage.Options{Editor: "vscode"})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"FROM invoices",        // the query
		"inv-1",                // its context
		"invoice",              // the dump label
		"number",               // the dumped value
		"invoice.viewed",       // the event
		"payments.test/charge", // the outbound call
		"req-abc",              // the request id
		"GET",                  // the method
		"/invoices/inv-1",      // the path
		"vscode://file",        // the open-in-editor link
		`<html lang="en">`,     // the product speaks English
		"development only",     // and says where it is allowed to exist
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the error page does not mention %q", want)
		}
	}
}

// TestRenderSuggestsTheCause is the difference between showing data and being
// useful: the page has to name the likely cause.
func TestRenderSuggestsTheCause(t *testing.T) {
	cases := []struct {
		name     string
		panicVal any
		prepare  func(*observability.Collector)
		want     string
	}{
		{
			name:     "missing grant",
			panicVal: errors.New("arandu: action not authorized: missing grant for auth.user.view"),
			want:     "security.Authorize",
		},
		{
			name:     "grant for another action",
			panicVal: errors.New("arandu: action not authorized: grant issued for a, used on b"),
			want:     "issued for one action",
		},
		{
			// Not a CSRF case. That one asserted a panic that cannot happen --
			// CSRFProtect answers 419 through http.Error and never panics -- and
			// the branch behind it matched "CSRFToken" too, so it answered a
			// missing method with somebody else's fix. See hints_test.go.
			name:     "the migrations never ran here",
			panicVal: errors.New(`ERROR: relation "posts" does not exist (SQLSTATE 42P01)`),
			want:     "aru migrate",
		},
		{
			name:     "n plus one",
			panicVal: errors.New("boom"),
			prepare: func(c *observability.Collector) {
				for range 5 {
					c.RecordQuery(`SELECT * FROM addresses WHERE user_id = $1`, nil, time.Millisecond, 1, nil)
				}
			},
			// The literal text is "Likely N+1", which html/template writes as
			// "N&#43;1", so the assertion matches the part that survives escaping.
			want: "the same statement ran 5 or more times",
		},
		{
			name:     "slow query",
			panicVal: errors.New("boom"),
			prepare: func(c *observability.Collector) {
				c.RecordQuery(`SELECT * FROM ledger`, nil, 900*time.Millisecond, 1, nil)
			},
			want: "Check the index",
		},
		{
			name:     "failed query before the panic",
			panicVal: errors.New("boom"),
			prepare: func(c *observability.Collector) {
				c.RecordQuery(`SELECT 1`, nil, time.Millisecond, 0, errors.New("connection reset"))
			},
			want: "A query failed before this panic",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			col := observability.NewCollector("req-1")
			if c.prepare != nil {
				c.prepare(col)
			}
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)

			errorpage.Render(rec, r, c.panicVal, col, errorpage.Options{})

			if body := rec.Body.String(); !strings.Contains(body, c.want) {
				t.Fatalf("no diagnosis mentioning %q", c.want)
			}
		})
	}
}

// TestSensitiveHeadersAreRedacted: a screenshot of an error page is the easiest
// way in the world to leak a session, so redaction applies even in development.
func TestSensitiveHeadersAreRedacted(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", "arandu_session=super-secret")
	r.Header.Set("Authorization", "Bearer super-secret")
	r.Header.Set("X-CSRF-Token", "super-secret")
	r.Header.Set("X-Arandu-Trace", "super-secret")
	r.Header.Set("Accept-Language", "en")

	rec := httptest.NewRecorder()
	errorpage.Render(rec, r, errors.New("boom"), nil, errorpage.Options{})

	body := rec.Body.String()
	if strings.Contains(body, "super-secret") {
		t.Fatal("a sensitive header value reached the page")
	}
	if !strings.Contains(body, "[redacted]") {
		t.Fatal("the page must show that a value was redacted, not hide the header")
	}
	if !strings.Contains(body, "Accept-Language") {
		t.Fatal("harmless headers must still be visible")
	}
}

// TestRenderWithoutCollector covers the panic that happens before Observe runs.
func TestRenderWithoutCollector(t *testing.T) {
	rec := httptest.NewRecorder()

	errorpage.Render(rec, httptest.NewRequest(http.MethodGet, "/", nil), "raw string panic", nil, errorpage.Options{})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "raw string panic") {
		t.Fatal("the page must show the panic value even with no Collector")
	}
}

func TestRenderDumpAnswersOK(t *testing.T) {
	col := observability.NewCollector("req-1")
	observability.Dump(observability.WithCollector(t.Context(), col), "checkpoint", 7)

	rec := httptest.NewRecorder()
	errorpage.RenderDump(rec, httptest.NewRequest(http.MethodGet, "/", nil), col, errorpage.Options{})

	// Dump-and-die is a deliberate stop, not a failure.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "checkpoint") {
		t.Fatal("the dump page must show the dump")
	}
}

func TestEditorLink(t *testing.T) {
	cases := map[string]string{
		"vscode":  "vscode://file/tmp/a.go:12",
		"cursor":  "cursor://file/tmp/a.go:12",
		"zed":     "zed://file/tmp/a.go:12",
		"goland":  "jetbrains://goland/navigate/reference?path=/tmp/a.go:12",
		"unknown": "vscode://file/tmp/a.go:12",
	}
	for editor, want := range cases {
		if got := errorpage.EditorLink(editor, "/tmp/a.go", 12); got != want {
			t.Errorf("EditorLink(%q) = %q, want %q", editor, got, want)
		}
	}
}

// TestCaptureMarksApplicationFrames: the page opens app frames and collapses
// framework and runtime ones, which is what makes a stack readable.
func TestCaptureMarksApplicationFrames(t *testing.T) {
	frames := errorpage.Capture(1, "github.com/arandu-io/framework/observability/errorpage_test")

	if len(frames) == 0 {
		t.Fatal("Capture returned no frames")
	}
	var firstApp *errorpage.StackFrame
	for i, f := range frames {
		if strings.HasPrefix(f.Func, "github.com/arandu-io/framework/observability/errorpage.") && f.IsApp {
			t.Errorf("framework frame marked as application code: %s", f.Func)
		}
		if strings.HasPrefix(f.Func, "runtime.") && f.IsApp {
			t.Errorf("runtime frame marked as application code: %s", f.Func)
		}
		if f.IsApp && firstApp == nil {
			firstApp = &frames[i]
		}
	}

	if firstApp == nil {
		t.Fatal("no frame was marked as application code")
	}
	// The snippet is what makes the page readable without leaving the browser.
	if firstApp.Snippet == nil {
		t.Fatalf("the application frame %s carries no source snippet", firstApp.Func)
	}
	if firstApp.SnipTop == 0 {
		t.Fatal("the snippet must know which line it starts at, or the highlight is meaningless")
	}
}
