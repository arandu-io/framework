// Package trace is the one format a browser test leaves behind when it fails:
// the steps it took, a PNG and the DOM after each one, the network, the
// console, the failures, and a section the server fills with what it did for
// each request.
//
// Every driver writes this format, because the page and not the driver writes
// it: a driver hands the page events and pixels, and the page records them
// here. A trace from one engine reads the same as a trace from another, so a
// tool that explains one explains all of them.
//
// A trace is written to a directory of its own per test:
//
//	<base>/<test name>/trace.json
//	<base>/<test name>/steps/001-visit.png
//	<base>/<test name>/steps/001-visit.html
//
// The base is ARANDU_E2E_TRACE_DIR when it is set and a directory under the
// system's temporary directory otherwise. It is written when the test failed,
// and after every test when ARANDU_E2E_TRACE=1.
package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Version is the format version written into every trace. A reader refuses a
// version it does not know rather than guessing at fields that moved.
const Version = 1

// FileName is the name of the JSON document inside a trace directory.
const FileName = "trace.json"

// Trace is the whole record of one test.
type Trace struct {
	Version  int       `json:"version"`
	Test     string    `json:"test"`
	Driver   string    `json:"driver,omitempty"`
	Browser  string    `json:"browser,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Failed   bool      `json:"failed"`

	Steps    []Step    `json:"steps"`
	Network  []Request `json:"network"`
	Console  []Message `json:"console"`
	Failures []Failure `json:"failures"`

	// Server is what the application did for each request, keyed by the
	// request id it answered with in X-Request-ID. A Request carries the same
	// id in ServerRequestID, which is the join.
	Server map[string]*Server `json:"server,omitempty"`
}

// Step is one thing the test did: a visit, a click, an expectation.
type Step struct {
	Index    int           `json:"index"`
	Action   string        `json:"action"`
	Target   string        `json:"target,omitempty"`
	URL      string        `json:"url,omitempty"`
	Started  time.Time     `json:"started"`
	Duration time.Duration `json:"duration_ns"`
	Error    string        `json:"error,omitempty"`

	// Screenshot and DOM are paths relative to the trace directory, empty when
	// the capture failed or was not taken.
	Screenshot string `json:"screenshot,omitempty"`
	DOM        string `json:"dom,omitempty"`

	// Data is whatever the step measured that is worth keeping as it came,
	// such as the geometry of an overflowing page.
	Data json.RawMessage `json:"data,omitempty"`
}

// Request is one network request the page made, with its answer.
type Request struct {
	ID              string    `json:"id"`
	Method          string    `json:"method"`
	URL             string    `json:"url"`
	Type            string    `json:"type,omitempty"`
	Status          int       `json:"status,omitempty"`
	ServerRequestID string    `json:"server_request_id,omitempty"`
	Started         time.Time `json:"started"`
	Finished        time.Time `json:"finished,omitempty"`
	Error           string    `json:"error,omitempty"`
}

// Message is one line of the page's console, or one entry of the browser's own
// log about the page.
type Message struct {
	Time   time.Time `json:"time"`
	Level  string    `json:"level"`
	Source string    `json:"source,omitempty"`
	Text   string    `json:"text"`
	URL    string    `json:"url,omitempty"`
}

// Failure is one reason the test failed that the test did not assert itself:
// an exception, a console error, a 5xx, a blocked resource, an overflow.
type Failure struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// Server is the server's side of one request.
type Server struct {
	Logs      []LogLine  `json:"logs,omitempty"`
	Queries   []Query    `json:"queries,omitempty"`
	Decisions []Decision `json:"decisions,omitempty"`
}

// LogLine is one log record the server wrote while answering the request.
type LogLine struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Query is one statement the server ran while answering the request.
type Query struct {
	SQL      string        `json:"sql"`
	Duration time.Duration `json:"duration_ns,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// Decision is one authorization decision: which policy answered which action,
// and the grant it issued or the reason it refused.
type Decision struct {
	Policy  string `json:"policy"`
	Action  string `json:"action"`
	Allowed bool   `json:"allowed"`
	Grant   string `json:"grant,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Recorder collects a trace while a test runs. It is safe for concurrent use:
// a driver delivers network and console events from a goroutine of its own.
type Recorder struct {
	mu    sync.Mutex
	trace Trace
	files map[string][]byte
	index map[string]int
}

// NewRecorder starts the trace of the named test.
func NewRecorder(test string) *Recorder {
	return &Recorder{
		trace: Trace{Version: Version, Test: test, Started: time.Now()},
		files: map[string][]byte{},
		index: map[string]int{},
	}
}

// SetDriver names the engine the trace was taken with.
func (r *Recorder) SetDriver(driver, browser string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Driver, r.trace.Browser = driver, browser
}

// Step records a step with the PNG and the DOM taken after it. Either capture
// may be nil. The index and the file names are assigned here, and the recorded
// step is returned.
func (r *Recorder) Step(s Step, screenshot, dom []byte) Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.Index = len(r.trace.Steps) + 1
	base := fmt.Sprintf("steps/%03d-%s", s.Index, slug(s.Action))
	if len(screenshot) > 0 {
		s.Screenshot = base + ".png"
		r.files[s.Screenshot] = screenshot
	}
	if len(dom) > 0 {
		s.DOM = base + ".html"
		r.files[s.DOM] = dom
	}
	r.trace.Steps = append(r.trace.Steps, s)
	return s
}

// Request records a request, or completes the one already recorded under the
// same id: a field left empty keeps what an earlier call wrote.
func (r *Recorder) Request(q Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.index[q.ID]
	if !ok {
		r.index[q.ID] = len(r.trace.Network)
		r.trace.Network = append(r.trace.Network, q)
		return
	}
	have := &r.trace.Network[i]
	if q.Method != "" {
		have.Method = q.Method
	}
	if q.URL != "" {
		have.URL = q.URL
	}
	if q.Type != "" {
		have.Type = q.Type
	}
	if q.Status != 0 {
		have.Status = q.Status
	}
	if q.ServerRequestID != "" {
		have.ServerRequestID = q.ServerRequestID
	}
	if !q.Started.IsZero() && have.Started.IsZero() {
		have.Started = q.Started
	}
	if !q.Finished.IsZero() {
		have.Finished = q.Finished
	}
	if q.Error != "" {
		have.Error = q.Error
	}
}

// Console records a console message.
func (r *Recorder) Console(m Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Console = append(r.trace.Console, m)
}

// Fail records a failure the test did not assert itself.
func (r *Recorder) Fail(f Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Failures = append(r.trace.Failures, f)
}

// Server adds what the server did for a request. Calls for the same id append
// to each other, so the log, the query recorder and the policy can each report
// on their own.
func (r *Recorder) Server(requestID string, s Server) {
	if requestID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.trace.Server == nil {
		r.trace.Server = map[string]*Server{}
	}
	have := r.trace.Server[requestID]
	if have == nil {
		have = &Server{}
		r.trace.Server[requestID] = have
	}
	have.Logs = append(have.Logs, s.Logs...)
	have.Queries = append(have.Queries, s.Queries...)
	have.Decisions = append(have.Decisions, s.Decisions...)
}

// Snapshot returns a copy of the trace as recorded so far.
func (r *Recorder) Snapshot() Trace {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.trace
	out.Steps = append([]Step(nil), r.trace.Steps...)
	out.Network = append([]Request(nil), r.trace.Network...)
	out.Console = append([]Message(nil), r.trace.Console...)
	out.Failures = append([]Failure(nil), r.trace.Failures...)
	if r.trace.Server != nil {
		out.Server = make(map[string]*Server, len(r.trace.Server))
		for id, s := range r.trace.Server {
			copied := *s
			out.Server[id] = &copied
		}
	}
	return out
}

// Write writes the trace into dir, replacing whatever an earlier run left
// there, and marks it failed or not. The JSON is written last, so a directory
// with a trace.json is a complete one.
func (r *Recorder) Write(dir string, failed bool) error {
	r.mu.Lock()
	r.trace.Failed = failed
	r.trace.Finished = time.Now()
	files := make(map[string][]byte, len(r.files))
	for name, body := range r.files {
		files[name] = body
	}
	r.mu.Unlock()
	snapshot := r.Snapshot()

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("trace: clear %s: %w", dir, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "steps"), 0o755); err != nil {
		return fmt.Errorf("trace: create %s: %w", dir, err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), files[name], 0o644); err != nil {
			return fmt.Errorf("trace: write %s: %w", name, err)
		}
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("trace: encode: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("trace: write %s: %w", FileName, err)
	}
	return nil
}

// Read reads the trace written into dir.
func Read(dir string) (*Trace, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	var t Trace
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("trace: decode %s: %w", filepath.Join(dir, FileName), err)
	}
	if t.Version != Version {
		return nil, fmt.Errorf("trace: %s has format version %d, this reader knows %d", dir, t.Version, Version)
	}
	return &t, nil
}

// Always reports whether a trace is written after every test and not only
// after a failing one, which ARANDU_E2E_TRACE=1 asks for.
func Always() bool {
	switch strings.ToLower(os.Getenv("ARANDU_E2E_TRACE")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Dir is the directory the trace of the named test is written to:
// ARANDU_E2E_TRACE_DIR, or arandu-e2e under the system's temporary directory,
// followed by the test's name with each subtest a directory of its own.
func Dir(test string) string {
	base := os.Getenv("ARANDU_E2E_TRACE_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "arandu-e2e")
	}
	parts := strings.Split(test, "/")
	for i, part := range parts {
		parts[i] = slug(part)
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

// slug keeps letters, digits, dot, dash and underscore, and turns every other
// run of characters into a single dash, so a step or test name is always one
// safe path segment.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "step"
	}
	if len(out) > 60 {
		out = strings.TrimRight(out[:60], "-.")
	}
	return out
}
