package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/observability"
)

// consoleWith returns a console holding one request built by fill.
func consoleWith(t *testing.T, fill func(*observability.Collector)) (*observability.Console, string) {
	t.Helper()

	recorder := observability.NewRecorder(10)
	col := observability.NewCollector("abc123")
	fill(col)
	recorder.Record(observability.Recorded{
		RequestID: "abc123",
		Method:    "GET",
		Path:      "/customers",
		Status:    200,
		Duration:  120 * time.Millisecond,
		At:        time.Now(),
		Collector: col,
	})
	return observability.NewConsole(recorder, "vscode"), "abc123"
}

func get(t *testing.T, c *observability.Console, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c.Handler(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestTheListShowsTheRequest(t *testing.T) {
	console, _ := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT * FROM customer", nil, 5*time.Millisecond, 10, nil)
	})

	rec := get(t, console, observability.ConsolePath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{"/customers", "GET", "200", "abc123"} {
		if !strings.Contains(body, want) {
			t.Errorf("the list does not show %q", want)
		}
	}
}

// TestAnEmptyConsoleSaysWhatToDo: "no data" with nothing else is the state a
// person hits first, and it should not read like a broken page.
func TestAnEmptyConsoleSaysWhatToDo(t *testing.T) {
	console := observability.NewConsole(observability.NewRecorder(10), "vscode")

	body := get(t, console, observability.ConsolePath).Body.String()
	if !strings.Contains(body, "Nothing recorded yet") {
		t.Errorf("the empty console does not explain itself:\n%s", body)
	}
}

// TestTheNPlusOneIsNamedAtTheTop is the whole point of the console. A page that
// makes you read the query table to notice the repetition only helps people who
// already knew what to look for.
func TestTheNPlusOneIsNamedAtTheTop(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT * FROM customer", nil, 2*time.Millisecond, 10, nil)
		for range 10 {
			c.RecordQuery("SELECT * FROM invoice WHERE customer_id = ?", nil, time.Millisecond, 1, nil)
		}
	})

	body := get(t, console, observability.ConsolePath+"/"+id).Body.String()

	// "N+1" reaches the page as "N&#43;1": html/template escapes the plus, and
	// the browser renders it back. Matching on the escaped form would tie this
	// test to the escaper, so it matches on the part that is not escaped -- and
	// the JSON test below checks the text a human reads.
	if !strings.Contains(body, "the same statement ran 10 times") {
		t.Fatalf("the console did not diagnose the N+1:\n%s", body)
	}
	if !strings.Contains(body, "FROM invoice") {
		t.Error("the diagnosis does not name the statement")
	}
	// The findings block comes before the query table, or it is not a diagnosis,
	// it is a footnote.
	if strings.Index(body, "the same statement ran") > strings.Index(body, "<h2>Queries") {
		t.Error("the diagnosis is rendered below the query table")
	}
}

func TestASlowQueryIsFlagged(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT * FROM invoice", nil, 400*time.Millisecond, 5000, nil)
	})

	body := get(t, console, observability.ConsolePath+"/"+id).Body.String()
	if !strings.Contains(body, "Slow query") {
		t.Errorf("a 400ms query was not flagged:\n%s", body)
	}
}

// TestTheTimelineIsRendered: "this page takes 800ms" leads to guessing. The
// breakdown is what turns it into an investigation.
func TestTheTimelineIsRendered(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT 1", nil, 90*time.Millisecond, 1, nil)
		c.RecordRender("customer/list.templ", 20*time.Millisecond)
		c.RecordExternal("GET", "https://api.example/rates", 200, 10*time.Millisecond)
	})

	body := get(t, console, observability.ConsolePath+"/"+id).Body.String()
	for _, want := range []string{"sql", "render", "external", "other", "90ms", "20ms", "10ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("the timeline does not show %q", want)
		}
	}
	// 90 of 120ms in the database is the finding that matters more than the bar.
	if !strings.Contains(body, "spent in the database") {
		t.Error("a request that is three quarters database did not say so")
	}
}

// TestTheOriginLinkOpensTheEditor: this is the line that saves the most time,
// and html/template silently rewrites an unknown scheme to #ZgotmplZ -- which
// turns every link on the page into a dead one with no hint why.
func TestTheOriginLinkOpensTheEditor(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT 1", nil, time.Millisecond, 1, nil)
	})

	body := get(t, console, observability.ConsolePath+"/"+id).Body.String()
	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("the editor link was rewritten by html/template: it needs template.URL")
	}
	if !strings.Contains(body, "vscode://file") {
		t.Errorf("no editor link in the page:\n%s", body)
	}
}

func TestEveryEditorHasAScheme(t *testing.T) {
	for editor, want := range map[string]string{
		"vscode": "vscode://file",
		"cursor": "cursor://file",
		"goland": "jetbrains://goland/navigate",
		"zed":    "zed://file",
		"":       "vscode://file", // unset falls back rather than emitting nothing
	} {
		got := observability.EditorLink(editor, "/src/app/main.go", 42)
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s = %q, want the %s scheme", editor, got, want)
		}
		if !strings.HasSuffix(got, "42") {
			t.Errorf("%s = %q, want it to carry the line number", editor, got)
		}
	}
}

func TestDumpsAndEventsAreShown(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		observability.Dump(observability.WithCollector(context.Background(), c), "the customer", map[string]any{"id": "c-1", "name": "Ana"})
		c.RecordEvent("customer.created", map[string]any{"id": "c-1"})
	})

	body := get(t, console, observability.ConsolePath+"/"+id).Body.String()
	for _, want := range []string{"the customer", "Ana", "customer.created"} {
		if !strings.Contains(body, want) {
			t.Errorf("the console does not show %q", want)
		}
	}
}

func TestAFailedQueryShowsItsError(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT * FROM customer", nil, time.Millisecond, 0, errors.New("no such table: customer"))
	})

	body := get(t, console, observability.ConsolePath+"/"+id).Body.String()
	if !strings.Contains(body, "no such table: customer") {
		t.Errorf("a failed query is shown as an ordinary row:\n%s", body)
	}
}

// TestAnUnknownIdExplainsTheBuffer: the id came from a log line, and "not
// found" alone leaves you wondering whether tracing is broken.
func TestAnUnknownIdExplainsTheBuffer(t *testing.T) {
	console, _ := consoleWith(t, func(*observability.Collector) {})

	rec := get(t, console, observability.ConsolePath+"/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "oldest are dropped") {
		t.Errorf("the page does not explain why the id is missing:\n%s", rec.Body.String())
	}
}

// TestTheJSONMatchesThePage is what `aru trace` depends on: one source, so the
// terminal and the page never disagree about what happened.
func TestTheJSONMatchesThePage(t *testing.T) {
	console, id := consoleWith(t, func(c *observability.Collector) {
		for range 6 {
			c.RecordQuery("SELECT * FROM invoice WHERE customer_id = ?", []any{"c-1"}, time.Millisecond, 1, nil)
		}
	})

	rec := get(t, console, observability.ConsolePath+"/"+id+"?format=json")
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}

	var payload struct {
		ID       string   `json:"ID"`
		Findings []string `json:"Findings"`
		Queries  []struct {
			SQL      string `json:"SQL"`
			Repeated int    `json:"Repeated"`
		} `json:"Queries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding: %v\n%s", err, rec.Body.String())
	}
	if payload.ID != id {
		t.Errorf("id = %q", payload.ID)
	}
	if len(payload.Findings) == 0 || !strings.Contains(payload.Findings[0], "N+1") {
		t.Errorf("findings = %v, want the N+1", payload.Findings)
	}
	if len(payload.Queries) != 6 || payload.Queries[0].Repeated != 6 {
		t.Errorf("queries = %+v", payload.Queries)
	}
}

func TestTheJSONListIsAlsoAvailable(t *testing.T) {
	console, _ := consoleWith(t, func(c *observability.Collector) {
		c.RecordQuery("SELECT 1", nil, time.Millisecond, 1, nil)
	})

	rec := get(t, console, observability.ConsolePath+"?format=json")
	var payload struct {
		Rows []struct {
			ID   string `json:"ID"`
			Path string `json:"Path"`
		} `json:"Rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(payload.Rows) != 1 || payload.Rows[0].Path != "/customers" {
		t.Fatalf("rows = %+v", payload.Rows)
	}
}

func TestAnUnknownIdInJSONIsNotFound(t *testing.T) {
	console, _ := consoleWith(t, func(*observability.Collector) {})

	rec := get(t, console, observability.ConsolePath+"/nope?format=json")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 so the CLI can tell the difference", rec.Code)
	}
}

// TestTheConsoleEscapesWhatItRenders: a path, a dump or a statement can carry
// anything, and the console renders it back. Without escaping, one request with
// a script tag in the query string turns the debug page into a way to run code
// in the developer's browser.
func TestTheConsoleEscapesWhatItRenders(t *testing.T) {
	recorder := observability.NewRecorder(10)
	col := observability.NewCollector("xss")
	observability.Dump(observability.WithCollector(context.Background(), col), "<script>alert(1)</script>", "<img src=x onerror=alert(1)>")
	recorder.Record(observability.Recorded{
		RequestID: "xss",
		Method:    "GET",
		Path:      "/customers?q=<script>alert(1)</script>",
		Status:    200,
		Duration:  time.Millisecond,
		At:        time.Now(),
		Collector: col,
	})
	console := observability.NewConsole(recorder, "vscode")

	for _, path := range []string{observability.ConsolePath, observability.ConsolePath + "/xss"} {
		body := get(t, console, path).Body.String()
		// The angle bracket is what decides it. The words can appear as text --
		// they are, after all, what the request contained.
		if strings.Contains(body, "<script>alert") {
			t.Errorf("%s rendered a script tag unescaped", path)
		}
		if strings.Contains(body, "<img src=x") {
			t.Errorf("%s rendered an img tag unescaped", path)
		}
	}
}
