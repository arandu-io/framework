package observability

import "net/http"

// HandleDebugConsole serves the debug console at /_arandu/debug.
//
// Scope for phase 3 (see docs/03-roadmap-fases.md): a ring buffer of the last N
// requests with their queries, dumps, events and jobs -- the Telescope
// equivalent, except core. The buffer lives in process memory by default and in
// the redis adapter when it is present, so it works with more than one pod.
//
// The Kernel only mounts this route when Env is dev.
func HandleDebugConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8">
<title>arandu console</title>
<body style="font:14px ui-monospace;background:#0d1117;color:#e6edf3;padding:40px">
<h1>arandu console</h1>
<p>Phase 3: request ring buffer, with the query, job and event inspector.</p>
</body></html>`))
}
