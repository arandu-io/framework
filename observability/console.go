package observability

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// ConsolePath is where the console is mounted.
const ConsolePath = "/_arandu/debug"

// Console serves the request inspector at /_arandu/debug.
//
// This is the Telescope equivalent, and it is core rather than a package you
// install. The reason is the thesis of the product: a framework whose selling
// point is "the debugger names the probable cause" cannot ship the debugger as
// an optional dependency that half the projects never add.
//
// It renders with html/template and no assets, like the error page and for the
// same reason: it has to work when the rest is broken, including when the view
// build failed.
type Console struct {
	recorder *Recorder
	// editor is the IDE the file links open, from config.Config.Editor.
	editor string
	// slowQuery is the threshold above which a query gets a badge.
	slowQuery time.Duration
	// nPlusOne is how many identical statements count as a suspected N+1.
	nPlusOne int
}

// NewConsole returns the console over a recorder.
func NewConsole(r *Recorder, editor string) *Console {
	return &Console{
		recorder:  r,
		editor:    editor,
		slowQuery: 100 * time.Millisecond,
		nPlusOne:  5,
	}
}

// Handler serves the list and the detail.
//
// One handler for both, because the router matches the prefix and the id under
// it, and splitting them would mean two routes to mount and one more thing to
// forget.
func (c *Console) Handler(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, ConsolePath), "/")

	if r.URL.Query().Get("format") == "json" {
		c.serveJSON(w, id)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if id == "" {
		c.render(w, consoleList, c.listData())
		return
	}

	entry, found := c.recorder.Find(id)
	if !found {
		w.WriteHeader(http.StatusNotFound)
		c.render(w, consoleMissing, map[string]any{"ID": id, "Kept": c.recorder.Len()})
		return
	}
	c.render(w, consoleDetail, c.detailData(entry))
}

func (c *Console) render(w http.ResponseWriter, t *template.Template, data any) {
	if err := t.Execute(w, data); err != nil {
		// The response is already partly written, so there is nothing useful to
		// show. The comment is for whoever is looking at the page source
		// wondering why it stopped halfway.
		fmt.Fprintf(w, "\n<!-- console render failed: %v -->", err)
	}
}

func (c *Console) listData() map[string]any {
	entries := c.recorder.Recent(0)
	rows := make([]map[string]any, 0, len(entries))

	for _, e := range entries {
		rows = append(rows, map[string]any{
			"ID":       e.RequestID,
			"Method":   e.Method,
			"Path":     e.Path,
			"Status":   e.Status,
			"Class":    e.StatusClass(),
			"Duration": ms(e.Duration),
			"At":       e.At.Format("15:04:05"),
			"Queries":  len(e.Collector.Queries),
			"SQL":      ms(e.Collector.QueryTime()),
			"NPlusOne": len(e.Collector.SuspectedNPlusOne(c.nPlusOne)) > 0,
			"Slow":     len(e.Collector.SlowQueries(c.slowQuery)) > 0,
			"Dumps":    len(e.Collector.Dumps),
		})
	}
	return map[string]any{"Rows": rows, "Empty": len(rows) == 0}
}

func (c *Console) detailData(e Recorded) map[string]any {
	col := e.Collector
	timeline := col.Timeline(e.Duration)
	repeated := col.SuspectedNPlusOne(c.nPlusOne)

	queries := make([]map[string]any, 0, len(col.Queries))
	for _, q := range col.Queries {
		queries = append(queries, map[string]any{
			"SQL":      collapse(q.SQL),
			"Args":     preview(q.Args),
			"Duration": ms(q.Duration),
			"Rows":     q.Rows,
			"Slow":     q.Duration >= c.slowQuery,
			"Repeated": repeated[q.SQL],
			"Origin":   fmt.Sprintf("%s:%d", short(q.Caller.File), q.Caller.Line),
			"Link":     template.URL(EditorLink(c.editor, q.Caller.File, q.Caller.Line)),
			"Error":    errorText(q.Err),
		})
	}

	dumps := make([]map[string]any, 0, len(col.Dumps))
	for _, d := range col.Dumps {
		dumps = append(dumps, map[string]any{
			"Label":  d.Label,
			"Value":  format(d.Value),
			"At":     ms(d.At),
			"Origin": fmt.Sprintf("%s:%d", short(d.Caller.File), d.Caller.Line),
			"Link":   template.URL(EditorLink(c.editor, d.Caller.File, d.Caller.Line)),
		})
	}

	events := make([]map[string]any, 0, len(col.Events))
	for _, ev := range col.Events {
		events = append(events, map[string]any{"Name": ev.Name, "Payload": format(ev.Payload), "At": ms(ev.At)})
	}

	external := make([]map[string]any, 0, len(col.External))
	for _, x := range col.External {
		external = append(external, map[string]any{
			"Method": x.Method, "URL": x.URL, "Status": x.Status, "Duration": ms(x.Duration),
		})
	}

	renders := make([]map[string]any, 0, len(col.Renders))
	for _, rr := range col.Renders {
		renders = append(renders, map[string]any{"Name": rr.Name, "Duration": ms(rr.Duration), "At": ms(rr.At)})
	}

	return map[string]any{
		"ID": e.RequestID, "Method": e.Method, "Path": e.Path,
		"Status": e.Status, "Class": e.StatusClass(),
		"Duration": ms(e.Duration), "At": e.At.Format("15:04:05"),
		"Timeline": []map[string]any{
			{"Name": "sql", "Value": ms(timeline.SQL), "Percent": timeline.Percent(timeline.SQL)},
			{"Name": "render", "Value": ms(timeline.Render), "Percent": timeline.Percent(timeline.Render)},
			{"Name": "external", "Value": ms(timeline.External), "Percent": timeline.Percent(timeline.External)},
			{"Name": "other", "Value": ms(timeline.Other), "Percent": timeline.Percent(timeline.Other)},
		},
		"Findings": c.findings(col, timeline),
		"Queries":  queries,
		"Dumps":    dumps,
		"Events":   events,
		"External": external,
		"Renders":  renders,
	}
}

// findings is the diagnosis, and it goes at the top of the page.
//
// A console that makes you read a table to notice the N+1 only helps people who
// already knew what to look for. This is the part that has to say the probable
// cause out loud.
func (c *Console) findings(col *Collector, timeline Timeline) []string {
	var out []string

	for sql, n := range col.SuspectedNPlusOne(c.nPlusOne) {
		// The origin points at the repository that issued it, not at the loop
		// that called the repository -- which is the honest limitation, and
		// saying so beats sending someone to the wrong file.
		out = append(out, fmt.Sprintf(
			"Likely N+1: the same statement ran %d times — %s", n, collapse(sql)))
	}

	for _, q := range col.SlowQueries(c.slowQuery) {
		out = append(out, fmt.Sprintf("Slow query, %s at %s:%d — %s",
			ms(q.Duration), short(q.Caller.File), q.Caller.Line, collapse(q.SQL)))
	}

	if len(col.Queries) > 0 && timeline.Percent(timeline.SQL) > 60 {
		out = append(out, fmt.Sprintf("%d%% of this request was spent in the database",
			timeline.Percent(timeline.SQL)))
	}
	if len(col.External) > 0 && timeline.Percent(timeline.External) > 40 {
		out = append(out, fmt.Sprintf("%d%% of this request was spent waiting on an outbound call",
			timeline.Percent(timeline.External)))
	}
	return out
}

// serveJSON is what `aru trace` reads.
//
// The console is the only source of this data, and a second endpoint for the
// CLI would be a second thing to keep in sync with what the page shows.
func (c *Console) serveJSON(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if id == "" {
		_ = enc.Encode(c.listData())
		return
	}
	entry, found := c.recorder.Find(id)
	if !found {
		w.WriteHeader(http.StatusNotFound)
		_ = enc.Encode(map[string]any{"error": "no request with that id", "id": id, "kept": c.recorder.Len()})
		return
	}
	_ = enc.Encode(c.detailData(entry))
}

func ms(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// collapse puts a statement on one line. Multi-line SQL in a table row makes the
// row unreadable, and the full text is one click away in the source anyway.
func collapse(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

// short keeps the last two path segments, which is enough to recognise a file
// and short enough to fit in a table.
func short(file string) string {
	if i := strings.LastIndex(file, "/"); i >= 0 {
		if j := strings.LastIndex(file[:i], "/"); j >= 0 {
			return file[j+1:]
		}
	}
	return file
}

func preview(args []any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, fmt.Sprintf("%v", a))
	}
	return strings.Join(parts, ", ")
}

func format(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
