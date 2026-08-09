// Package errorpage renders the development error page.
//
// It is the functional equivalent of Ignition, with one difference in scope:
// here the data comes from the Collector, which is part of the core, so the page
// knows the queries, the dumps, the events and the routes without any extra
// package installed.
//
// Absolute rule: nothing in this package may be reachable when Env is not dev.
// The Recover middleware is the only caller, and it checks the flag first.
package errorpage

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arandu-io/framework/observability"
)

// Options carries what the page needs from the application configuration.
type Options struct {
	// Editor is the target of the "open in IDE" links: vscode, cursor, goland
	// or zed.
	Editor string
	// AppModule is the module path of the application, used to tell app frames
	// from framework and stdlib frames.
	AppModule string
	// Diagnose collects what the registered modules have to say about the state
	// of the system right now. Pass kernel.Diagnose.
	//
	// It exists because the most useful hint is often about something that
	// happened outside this request: the outbox has been stuck for four minutes,
	// the scheduler last ran an hour ago. A page that only looks at the request
	// cannot see any of it, and that is exactly the state where somebody is
	// staring at an error wondering what changed.
	Diagnose func(ctx context.Context) []string
}

type viewData struct {
	// Title is the headline: what happened, in the words of whatever failed.
	//
	// It used to be the Go type of the panic value, so the biggest text on the
	// page read "*errors.errorString" -- true, and useless. The product's whole
	// claim is a debug page that names the probable cause; the headline is the
	// first thing it says. Found by audit.
	Title string
	// Kind is the Go type, kept as the subtitle: it matters when the message is
	// generic, and it is never what somebody reads first.
	Kind      string
	Message   string
	RequestID string
	Method    string
	Path      string
	Frames    []StackFrame
	Queries   []observability.QueryRecord
	Dumps     []observability.DumpRecord
	Events    []observability.EventRecord
	External  []observability.ExternalRecord
	NPlusOne  map[string]int
	Headers   map[string][]string
	Elapsed   time.Duration
	QueryTime time.Duration
	Hints     []string
	Editor    string
}

// headline turns whatever was panicked with into one line somebody can read.
//
// The first line only, and bounded: a panic value carrying a stack trace or a
// serialized payload would otherwise push everything else off the screen.
func headline(v any) string {
	text := strings.TrimSpace(fmt.Sprint(v))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if text == "" {
		// Something was panicked with that prints as nothing. The type is all
		// there is, and it beats an empty headline.
		return fmt.Sprintf("%T", v)
	}
	const limit = 140
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

// Render draws the full error page.
func Render(w http.ResponseWriter, r *http.Request, panicValue any, col *observability.Collector, opts Options) {
	d := viewData{
		Title:   headline(panicValue),
		Kind:    fmt.Sprintf("%T", panicValue),
		Message: fmt.Sprint(panicValue),
		Method:  r.Method,
		Path:    r.URL.Path,
		Frames:  Capture(5, opts.AppModule),
		Headers: redact(r.Header),
		Editor:  opts.Editor,
	}
	if col != nil {
		d.RequestID = col.RequestID
		d.Queries, d.Dumps, d.Events, d.External = col.Queries(), col.Dumps(), col.Events(), col.External()
		d.NPlusOne = col.SuspectedNPlusOne(nPlusOneThreshold)
		d.Elapsed = time.Since(col.Start)
		d.QueryTime = col.QueryTime()
	}
	d.Hints = hints(d)
	if opts.Diagnose != nil {
		d.Hints = append(d.Hints, opts.Diagnose(r.Context())...)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = tmpl.Execute(w, d)
}

// RenderDump draws the dump page, for the DumpDie flow. It answers 200: the
// request was aborted on purpose, not by a failure.
func RenderDump(w http.ResponseWriter, r *http.Request, col *observability.Collector, opts Options) {
	d := viewData{Title: "Dump", Kind: "dump", Method: r.Method, Path: r.URL.Path, Editor: opts.Editor}
	if col != nil {
		d.RequestID, d.Dumps, d.Queries = col.RequestID, col.Dumps(), col.Queries()
		d.Elapsed = time.Since(col.Start)
		d.QueryTime = col.QueryTime()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, d)
}

const (
	nPlusOneThreshold = 5
	slowQuery         = 200 * time.Millisecond
)

// hints is the part that copies the best idea in Ignition: do not just show the
// error, suggest the likely cause. Every pattern recognized here saves an hour
// of support later, and the list grows with each bug we hit ourselves.
func hints(d viewData) []string {
	var out []string

	if len(d.NPlusOne) > 0 {
		out = append(out, fmt.Sprintf("Likely N+1: the same statement ran %d or more times in this request. Load it in one batch inside the repository.", nPlusOneThreshold))
	}
	for _, q := range d.Queries {
		if q.Duration >= slowQuery {
			out = append(out, "A query took "+q.Duration.String()+". Check the index for: "+truncate(q.SQL, 60))
			break
		}
	}

	// A failed query is usually the panic itself: the repository returns the
	// driver error, the action returns it, and the handler panics with it.
	// Printing it again under Diagnosis said the same sentence twice, once as
	// the headline and once as a finding, which reads as two problems. Say it
	// only when the headline is about something else -- a query that failed, was
	// handled, and caused a later panic is exactly the case worth naming.
	for _, q := range d.Queries {
		if q.Err != nil && !strings.Contains(d.Message, q.Err.Error()) {
			out = append(out, "A query failed before this panic: "+q.Err.Error())
			break
		}
	}

	if h, ok := messageHint(d.Message); ok {
		out = append(out, h)
	}
	return out
}

// messageHint reads the panic message and returns at most one sentence.
//
// At most one, and narrowest first. These are ordered by how much of the message
// they require, because a broad substring above a narrow one answers the narrow
// failure with the wrong fix.
//
// That is not hypothetical. The branch that used to be here matched "CSRF", so
// it also matched "CSRFToken" -- and the one message reaching this page that
// contains that word is the view engine saying "@csrf needs the page data to
// provide the token. Add a CSRFToken() string method". Somebody whose page
// struct was missing a method was told to go edit hx-headers on their base
// template. It fired on any panic naming *security.CSRF too. security.ErrCSRF
// itself never arrives here: CSRFProtect answers 419 through http.Error and
// does not panic, so the branch had no correct trigger at all.
//
// A pattern earns its place by three tests: it names the failure in the words of
// the reader's own project, it names the one command or line that fixes it, and
// it cannot fire on a different problem. The third is the expensive one -- a
// hint that guesses wrong costs more than no hint, because the reader spends the
// next hour in the file it named.
func messageHint(msg string) (string, bool) {
	// A service was called with the zero security.Subject, because the session
	// was never loaded or SessionStore.Load's error was dropped. Authorize
	// refuses before it consults a policy, so the policy for this action never
	// ran -- and the developer opens it, edits it, and nothing they change has
	// any effect.
	//
	// The anchor carries "not authorized: " on purpose. Authorize's other branch
	// interpolates the application's own denial text verbatim, so a policy that
	// wrote "the anonymous subject on this blog may not comment" would trip a
	// bare match -- and there the policy did run, which is the opposite of what
	// this says.
	if strings.Contains(msg, "not authorized: anonymous subject on") {
		return "No policy ran. security.Authorize refuses a Subject with no ID before it consults one, so the policy for this action was never called and editing it changes nothing. Give the action a subject: security.Guest(tenant) when the page is meant to be readable without an account, or the Subject from SessionStore.Load with its error handled.", true
	}

	if strings.Contains(msg, "missing grant") {
		return "You reached a repository without going through security.Authorize. Authorize the action first: the Grant is what proves a policy ran.", true
	}

	if strings.Contains(msg, "grant issued for") {
		return "The Grant was issued for one action and used on another. Check the action constant in the repository method.", true
	}

	// The statement names a relation the database cannot resolve: most often a
	// project whose migrations have never run against this database, which is
	// the highest-value case here and the one this page had nothing to say
	// about.
	//
	// It states what is true and offers the check rather than asserting the
	// cause, because the same text has more than one cause and none of them is
	// separable from the string: a missing sequence, a misspelled CTE, a trigger
	// body naming a missing table and a table in another schema all render
	// identically.
	if name, ok := relationNotFound(msg); ok {
		return "Nothing in this database resolves the name " + strconv.Quote(name) + ". If the migrations have never run against it, `aru migrate` creates it. If `aru migrate` answers \"nothing to migrate\", no migration in this project declares that name, and the statement above is naming something else: a CTE, a sequence, or a table in another schema.", true
	}

	return "", false
}

// relationNotFound reports the relation a driver said does not exist, for the
// three engines, and false when the message is about something else.
//
// The anchors are long deliberately. PostgreSQL's SQLSTATE 42P01 is the whole
// undefined_table class, and an alias typo lives in it too: `SELECT p.id FROM
// posts` answers `missing FROM-clause entry for table "p" (SQLSTATE 42P01)`,
// where `aru migrate` is not the fix and the name captured would be a query
// alias. A framework whose data path is hand-written SQL sees that far more
// often than a missing migration.
func relationNotFound(msg string) (string, bool) {
	for _, re := range relationPatterns {
		if m := re.FindStringSubmatch(msg); m != nil {
			return m[1], true
		}
	}
	return "", false
}

var relationPatterns = []*regexp.Regexp{
	// PostgreSQL: relation "posts" does not exist (SQLSTATE 42P01)
	regexp.MustCompile(`relation "([^"]+)" does not exist`),
	// MySQL 1146. 1051 ("Unknown table", from DROP) is deliberately excluded:
	// it is a different failure with a different fix.
	regexp.MustCompile(`Error 1146 \(42S02\): Table '(?:[^.']*\.)?([^']+)' doesn't exist`),
	// SQLite
	regexp.MustCompile(`no such table: ([A-Za-z_][A-Za-z0-9_.]*)`),
}

// redact hides sensitive headers even in development: a screenshot of an error
// page leaks a session cookie with absurd ease.
func redact(h http.Header) map[string][]string {
	sensitive := map[string]bool{
		"Cookie": true, "Set-Cookie": true, "Authorization": true,
		"X-Csrf-Token": true, "X-Arandu-Trace": true, "Proxy-Authorization": true,
	}
	out := map[string][]string{}
	for k, v := range h {
		if sensitive[http.CanonicalHeaderKey(k)] {
			out[k] = []string{"[redacted]"}
			continue
		}
		out[k] = v
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var tmpl = template.Must(template.New("errorpage").Funcs(template.FuncMap{
	// The return type must be template.URL: html/template only trusts a short
	// list of schemes in an href, and it rewrites everything else to
	// "#ZgotmplZ". Without this, every "open in editor" link on the page is
	// silently dead, because vscode:// and zed:// are not on that list.
	"editorLink": func(editor, file string, line int) template.URL {
		return template.URL(EditorLink(editor, file, line))
	},
	"isSlow": func(d time.Duration) bool { return d >= slowQuery },
}).Parse(pageHTML))

const pageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>{{.Title}} — arandu</title>
<style>
:root{color-scheme:dark;--bg:#0d1117;--panel:#161b22;--line:#30363d;--fg:#e6edf3;--dim:#8b949e;--red:#f85149;--amber:#d29922;--accent:#58a6ff}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace}
header{background:var(--panel);border-bottom:1px solid var(--line);padding:20px 28px}
h1{margin:0;font-size:16px;color:var(--red)}h1 span{color:var(--dim);font-weight:400}
.msg{margin-top:8px;font-size:18px;color:var(--fg)}
.meta{margin-top:10px;color:var(--dim);font-size:12px}
main{padding:20px 28px;max-width:1200px}
section{margin-bottom:28px}
h2{font-size:12px;text-transform:uppercase;letter-spacing:.08em;color:var(--dim);border-bottom:1px solid var(--line);padding-bottom:6px}
.hint{background:rgba(210,153,34,.12);border-left:3px solid var(--amber);padding:10px 14px;margin:8px 0;border-radius:0 4px 4px 0}
.frame{border:1px solid var(--line);border-radius:6px;margin-bottom:10px;overflow:hidden}
.frame.vendor{opacity:.45}
.frame>summary{padding:8px 12px;background:var(--panel);cursor:pointer;list-style:none}
.frame .fn{color:var(--accent)}.frame .loc{color:var(--dim);font-size:12px}
.frame a{color:var(--dim)}
pre{margin:0;padding:12px;overflow-x:auto;background:#010409}
table{width:100%;border-collapse:collapse;font-size:13px}
td,th{text-align:left;padding:6px 10px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--dim);font-weight:500}
.slow{color:var(--amber)}
.err{color:var(--red)}
</style></head><body>
<header>
  <h1>{{.Title}} <span>{{if .Kind}}{{.Kind}} — {{end}}arandu debug (development only)</span></h1>
  <div class="msg">{{.Message}}</div>
  <div class="meta">{{.Method}} {{.Path}} · request_id {{.RequestID}} · {{.Elapsed}} total · {{.QueryTime}} in SQL · {{len .Queries}} queries</div>
</header>
<main>

{{if .Hints}}<section><h2>Diagnosis</h2>
  {{range .Hints}}<div class="hint">{{.}}</div>{{end}}
</section>{{end}}

<section><h2>Stack</h2>
  {{range .Frames}}
  <details class="frame {{if not .IsApp}}vendor{{end}}" {{if .IsApp}}open{{end}}>
    <summary><span class="fn">{{.Func}}</span><br>
      <span class="loc">{{.File}}:{{.Line}}</span>
      <a href="{{editorLink $.Editor .File .Line}}">open in editor</a>
    </summary>
    {{if .Snippet}}<pre>{{range .Snippet}}{{.}}
{{end}}</pre>{{end}}
  </details>
  {{end}}
</section>

{{if .Queries}}<section><h2>Queries</h2><table>
  <tr><th>SQL</th><th>Time</th><th>Rows</th><th>Origin</th></tr>
  {{range .Queries}}<tr>
    <td class="{{if .Err}}err{{end}}">{{.SQL}}{{if .Err}}<br>{{.Err}}{{end}}</td>
    <td class="{{if isSlow .Duration}}slow{{end}}">{{.Duration}}</td>
    <td>{{.Rows}}</td>
    <td class="loc">{{.Caller.File}}:{{.Caller.Line}}</td>
  </tr>{{end}}
</table></section>{{end}}

{{if .Dumps}}<section><h2>Dumps</h2><table>
  <tr><th>Label</th><th>Value</th><th>Origin</th><th>At</th></tr>
  {{range .Dumps}}<tr><td>{{.Label}}</td><td>{{printf "%+v" .Value}}</td>
  <td class="loc">{{.Caller.File}}:{{.Caller.Line}}</td><td>{{.At}}</td></tr>{{end}}
</table></section>{{end}}

{{if .Events}}<section><h2>Events</h2><table>
  <tr><th>Name</th><th>Payload</th><th>At</th></tr>
  {{range .Events}}<tr><td>{{.Name}}</td><td>{{printf "%+v" .Payload}}</td><td>{{.At}}</td></tr>{{end}}
</table></section>{{end}}

{{if .External}}<section><h2>Outbound calls</h2><table>
  <tr><th>Method</th><th>URL</th><th>Status</th><th>Time</th></tr>
  {{range .External}}<tr><td>{{.Method}}</td><td>{{.URL}}</td><td>{{.Status}}</td><td>{{.Duration}}</td></tr>{{end}}
</table></section>{{end}}

<section><h2>Request headers</h2><table>
  {{range $k, $v := .Headers}}<tr><th>{{$k}}</th><td>{{range $v}}{{.}} {{end}}</td></tr>{{end}}
</table></section>

</main></body></html>`
