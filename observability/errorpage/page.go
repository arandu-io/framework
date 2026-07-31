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
	Title     string
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

// Render draws the full error page.
func Render(w http.ResponseWriter, r *http.Request, panicValue any, col *observability.Collector, opts Options) {
	d := viewData{
		Title:   fmt.Sprintf("%T", panicValue),
		Message: fmt.Sprint(panicValue),
		Method:  r.Method,
		Path:    r.URL.Path,
		Frames:  Capture(5, opts.AppModule),
		Headers: redact(r.Header),
		Editor:  opts.Editor,
	}
	if col != nil {
		d.RequestID = col.RequestID
		d.Queries, d.Dumps, d.Events, d.External = col.Queries, col.Dumps, col.Events, col.External
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
	d := viewData{Title: "Dump", Method: r.Method, Path: r.URL.Path, Editor: opts.Editor}
	if col != nil {
		d.RequestID, d.Dumps, d.Queries = col.RequestID, col.Dumps, col.Queries
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
	for _, q := range d.Queries {
		if q.Err != nil {
			out = append(out, "A query failed before this panic: "+q.Err.Error())
			break
		}
	}
	if strings.Contains(d.Message, "missing grant") {
		out = append(out, "You reached a repository without going through security.Authorize. Authorize the action first: the Grant is what proves a policy ran.")
	}
	if strings.Contains(d.Message, "grant issued for") {
		out = append(out, "The Grant was issued for one action and used on another. Check the action constant in the repository method.")
	}
	if strings.Contains(d.Message, "CSRF") {
		out = append(out, `The base template is missing hx-headers with X-CSRF-Token on <body>. Without it every HTMX request that changes state fails.`)
	}
	return out
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
  <h1>{{.Title}} <span>— arandu debug (development only)</span></h1>
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
