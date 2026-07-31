package observability

import "html/template"

// The console markup, inline.
//
// No stylesheet, no script, no embedded asset. The console has to render when
// the rest of the project is broken -- including when the view build failed --
// which is the same reason the error page is built this way. It is also what
// keeps the promise that a project runs with `git clone && aru dev` honest: the
// framework's own pages never needed a build step.

const consoleStyle = `
:root { color-scheme: dark }
* { box-sizing: border-box }
body {
  margin: 0; padding: 32px 40px;
  font: 14px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  background: #0d1117; color: #e6edf3;
}
a { color: #58a6ff; text-decoration: none }
a:hover { text-decoration: underline }
h1 { font-size: 18px; font-weight: 600; margin: 0 0 4px }
h2 { font-size: 13px; font-weight: 600; text-transform: uppercase;
     letter-spacing: .08em; color: #8b949e; margin: 32px 0 12px }
.sub { color: #8b949e; margin: 0 0 24px }
table { width: 100%; border-collapse: collapse; font-size: 13px }
th { text-align: left; font-weight: 500; color: #8b949e; padding: 6px 12px 6px 0;
     border-bottom: 1px solid #21262d; white-space: nowrap }
td { padding: 8px 12px 8px 0; border-bottom: 1px solid #161b22; vertical-align: top }
td.num { text-align: right; white-space: nowrap; color: #8b949e }
tr:hover td { background: #161b22 }
code { color: #e6edf3; word-break: break-word }
.tag { display: inline-block; padding: 1px 6px; border-radius: 3px;
       font-size: 11px; font-weight: 600; letter-spacing: .02em }
.ok { background: #10321c; color: #56d364 }
.redirect { background: #1c2b3a; color: #58a6ff }
.client-error { background: #3a2d10; color: #e3b341 }
.server-error { background: #3d1a1a; color: #f85149 }
.warn { background: #3a2d10; color: #e3b341 }
.bad { background: #3d1a1a; color: #f85149 }
.findings { border: 1px solid #3d2a10; background: #1a1409; border-radius: 6px;
            padding: 14px 18px; margin: 0 0 28px }
.findings p { margin: 0 0 6px; color: #e3b341 }
.findings p:last-child { margin: 0 }
.bar { display: flex; height: 8px; border-radius: 4px; overflow: hidden;
       background: #161b22; margin: 0 0 10px }
.bar span { display: block }
.legend { display: flex; gap: 20px; font-size: 12px; color: #8b949e;
          flex-wrap: wrap; margin: 0 0 8px }
.legend b { color: #e6edf3; font-weight: 500 }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 2px;
       margin-right: 6px; vertical-align: middle }
.sql { background: #58a6ff } .render { background: #56d364 }
.external { background: #e3b341 } .other { background: #30363d }
pre { margin: 0; padding: 10px 12px; background: #161b22; border-radius: 4px;
      overflow-x: auto; font-size: 12px; color: #c9d1d9 }
.empty { color: #8b949e; padding: 32px 0 }
.muted { color: #8b949e }
`

var consoleList = template.Must(template.New("list").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>arandu console</title><style>` + consoleStyle + `</style></head>
<body>
<h1>arandu console</h1>
<p class="sub">The last requests this process handled. Newest first.</p>

{{if .Empty}}
<p class="empty">Nothing recorded yet. Make a request and reload.</p>
{{else}}
<table>
  <tr>
    <th>time</th><th>method</th><th>path</th><th>status</th>
    <th class="num">duration</th><th class="num">queries</th><th class="num">sql</th><th></th>
  </tr>
  {{range .Rows}}
  <tr>
    <td class="muted">{{.At}}</td>
    <td>{{.Method}}</td>
    <td><a href="` + ConsolePath + `/{{.ID}}">{{.Path}}</a></td>
    <td><span class="tag {{.Class}}">{{.Status}}</span></td>
    <td class="num">{{.Duration}}</td>
    <td class="num">{{.Queries}}</td>
    <td class="num">{{.SQL}}</td>
    <td>
      {{if .NPlusOne}}<span class="tag bad">N+1</span> {{end}}
      {{if .Slow}}<span class="tag warn">slow</span> {{end}}
      {{if .Dumps}}<span class="tag redirect">{{.Dumps}} dump</span>{{end}}
    </td>
  </tr>
  {{end}}
</table>
{{end}}
</body></html>`))

var consoleDetail = template.Must(template.New("detail").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>{{.Method}} {{.Path}} — arandu console</title><style>` + consoleStyle + `</style></head>
<body>
<p class="sub"><a href="` + ConsolePath + `">&larr; all requests</a></p>
<h1>{{.Method}} {{.Path}} <span class="tag {{.Class}}">{{.Status}}</span></h1>
<p class="sub">{{.ID}} &middot; {{.At}} &middot; {{.Duration}}</p>

{{if .Findings}}
<div class="findings">
  {{range .Findings}}<p>{{.}}</p>{{end}}
</div>
{{end}}

<h2>Timeline</h2>
<div class="bar">
  {{range .Timeline}}<span class="{{.Name}}" style="width:{{.Percent}}%"></span>{{end}}
</div>
<div class="legend">
  {{range .Timeline}}<div><span class="dot {{.Name}}"></span>{{.Name}} <b>{{.Value}}</b> ({{.Percent}}%)</div>{{end}}
</div>

<h2>Queries ({{len .Queries}})</h2>
{{if .Queries}}
<table>
  <tr><th>statement</th><th>args</th><th class="num">rows</th><th class="num">time</th><th>origin</th><th></th></tr>
  {{range .Queries}}
  <tr>
    <td><code>{{.SQL}}</code>{{if .Error}}<br><span class="tag bad">{{.Error}}</span>{{end}}</td>
    <td class="muted">{{.Args}}</td>
    <td class="num">{{.Rows}}</td>
    <td class="num">{{.Duration}}</td>
    <td><a href="{{.Link}}">{{.Origin}}</a></td>
    <td>
      {{if .Slow}}<span class="tag warn">slow</span> {{end}}
      {{if .Repeated}}<span class="tag bad">&times;{{.Repeated}}</span>{{end}}
    </td>
  </tr>
  {{end}}
</table>
{{else}}<p class="empty">No queries.</p>{{end}}

{{if .Dumps}}
<h2>Dumps ({{len .Dumps}})</h2>
{{range .Dumps}}
<p class="muted">{{.Label}} &middot; {{.At}} &middot; <a href="{{.Link}}">{{.Origin}}</a></p>
<pre>{{.Value}}</pre>
{{end}}
{{end}}

{{if .Renders}}
<h2>Renders ({{len .Renders}})</h2>
<table>
  <tr><th>template</th><th class="num">at</th><th class="num">time</th></tr>
  {{range .Renders}}<tr><td>{{.Name}}</td><td class="num">{{.At}}</td><td class="num">{{.Duration}}</td></tr>{{end}}
</table>
{{end}}

{{if .External}}
<h2>Outbound calls ({{len .External}})</h2>
<table>
  <tr><th>method</th><th>url</th><th class="num">status</th><th class="num">time</th></tr>
  {{range .External}}<tr><td>{{.Method}}</td><td><code>{{.URL}}</code></td><td class="num">{{.Status}}</td><td class="num">{{.Duration}}</td></tr>{{end}}
</table>
{{end}}

{{if .Events}}
<h2>Events ({{len .Events}})</h2>
{{range .Events}}
<p class="muted">{{.Name}} &middot; {{.At}}</p>
<pre>{{.Payload}}</pre>
{{end}}
{{end}}
</body></html>`))

var consoleMissing = template.Must(template.New("missing").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>not recorded — arandu console</title><style>` + consoleStyle + `</style></head>
<body>
<p class="sub"><a href="` + ConsolePath + `">&larr; all requests</a></p>
<h1>No request with id {{.ID}}</h1>
<p class="sub">
  The buffer holds {{.Kept}} request(s), and the oldest are dropped as new ones
  arrive. If this id came from a log line written a while ago, it is gone --
  reproduce the request and look again.
</p>
</body></html>`))
