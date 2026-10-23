// Package servertrace records what the server did for each request a test
// sent -- the lines it logged, the statements it ran and the Policy decisions
// it was answered -- under the request id the response carries.
//
// It is the server's half of a test trace. A browser sees a request go out and
// an answer come back with an X-Request-ID header; the section recorded under
// that id says what happened in between, so a failing journey shows the denied
// Policy or the statement that failed next to the click that caused it.
//
// The Recorder wraps the application's whole handler, outside everything the
// application installs:
//
//	trace := servertrace.New()
//	server := httptest.NewServer(trace.Middleware(app.Handler()))
//	// ... drive the application ...
//	section, ok := trace.Section(requestID)
//
// # Where the records come from
//
// Nothing here instruments the application. Every record is read from the
// request's log.Collector, which the database handle already writes its
// statements into, the request logger writes its lines into, and the code
// that runs a policy writes its decision into. The middleware installs a
// collector for the request, so the records exist outside development too, and
// when the application installs its own further in -- development does -- the
// two are read together.
//
// The request id is the one the application answers with. A request that
// arrives without a valid one is given one before the application sees it, so
// the application adopts it and every line it logs carries it.
//
// # What a section leaves out
//
// The values bound to a statement are counted, not kept: they are rows of the
// application's data -- a password hash, an address -- and a trace is an
// artifact a CI run uploads. The statement and where it was issued from are
// what finding the problem takes.
package servertrace

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/arandu-io/hesape/log"
)

// Header is the response header that carries the request id, and the request
// header a client may send one in.
const Header = "X-Request-ID"

// Section is what the server did for one request.
type Section struct {
	RequestID  string     `json:"request_id"`
	Method     string     `json:"method"`
	Path       string     `json:"path"`
	Status     int        `json:"status"`
	Started    time.Time  `json:"started"`
	DurationMS float64    `json:"duration_ms"`
	Logs       []Line     `json:"logs"`
	Queries    []Query    `json:"queries"`
	Decisions  []Decision `json:"decisions"`
}

// Line is one line the request logged.
type Line struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Query is one statement the request ran.
type Query struct {
	SQL        string  `json:"sql"`
	Args       int     `json:"args"`
	DurationMS float64 `json:"duration_ms"`
	// Rows is how many rows the statement read or wrote, or -1 when the
	// driver did not say.
	Rows   int    `json:"rows"`
	Error  string `json:"error,omitempty"`
	Caller string `json:"caller,omitempty"`
}

// Decision is one Policy decision: who asked, for which action, and whether a
// Grant was issued.
type Decision struct {
	Subject string `json:"subject"`
	Tenant  string `json:"tenant"`
	Action  string `json:"action"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	Caller  string `json:"caller,omitempty"`
}

// Recorder keeps a section for every request that went through its
// middleware. It is safe for concurrent use.
type Recorder struct {
	mu       sync.Mutex
	sections []Section
	index    map[string]int
}

// New returns an empty recorder.
func New() *Recorder { return &Recorder{index: map[string]int{}} }

// Section returns the section recorded under a request id.
func (r *Recorder) Section(requestID string) (Section, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.index[requestID]
	if !ok {
		return Section{}, false
	}
	return r.sections[i], true
}

// Sections returns every recorded section, in the order the requests finished.
func (r *Recorder) Sections() []Section {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Section(nil), r.sections...)
}

// Middleware records a section for every request that goes through next. It
// goes outside everything the application installs, so the collector it
// reserves is the one the application's own fills.
func (r *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := req.Header.Get(Header)
		if !validID(id) {
			id = newID()
			req = req.Clone(req.Context())
			req.Header.Set(Header, id)
		}

		// The slot is reserved before the request's own collector is
		// installed, so it ends up holding the collector the request held
		// last: the application's, when it installs one, or this one.
		reserved := log.WithCollectorSlot(req.Context())
		own := log.NewCollector(id)
		ctx := log.WithCollector(reserved, own)

		writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(writer, req.WithContext(ctx))
		duration := time.Since(start)

		if answered := writer.Header().Get(Header); validID(answered) {
			id = answered
		}
		collectors := []*log.Collector{own}
		if last := log.FromContext(reserved); last != nil && last != own {
			collectors = append(collectors, last)
		}
		r.store(section(id, req, writer.status, start, duration, collectors))
	})
}

func (r *Recorder) store(s Section) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i, ok := r.index[s.RequestID]; ok {
		r.sections[i] = s
		return
	}
	r.index[s.RequestID] = len(r.sections)
	r.sections = append(r.sections, s)
}

// section reads what the collectors hold into one section, the lines in the
// order they were written.
func section(id string, req *http.Request, status int, start time.Time, duration time.Duration, collectors []*log.Collector) Section {
	s := Section{
		RequestID: id, Method: req.Method, Path: req.URL.Path, Status: status,
		Started: start, DurationMS: milliseconds(duration),
		Logs: []Line{}, Queries: []Query{}, Decisions: []Decision{},
	}
	for _, c := range collectors {
		for _, l := range c.Logs() {
			s.Logs = append(s.Logs, Line{Time: l.Time, Level: l.Level.String(), Message: l.Message, Attrs: l.Attrs})
		}
		for _, q := range c.Queries() {
			query := Query{SQL: q.SQL, Args: len(q.Args), DurationMS: milliseconds(q.Duration), Rows: q.Rows, Caller: frame(q.Caller)}
			if q.Err != nil {
				query.Error = q.Err.Error()
			}
			s.Queries = append(s.Queries, query)
		}
		for _, d := range c.Decisions() {
			s.Decisions = append(s.Decisions, Decision{
				Subject: d.Subject, Tenant: d.Tenant, Action: d.Action, Allowed: d.Allowed, Reason: d.Reason, Caller: frame(d.Caller),
			})
		}
	}
	sort.SliceStable(s.Logs, func(i, j int) bool { return s.Logs[i].Time.Before(s.Logs[j].Time) })
	return s
}

func frame(f log.Frame) string {
	if f.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// validID accepts what the application accepts from a client: short and
// hexadecimal, with dashes. Anything else would be replaced by the
// application, and the section would be filed under an id the response does
// not carry.
func validID(v string) bool {
	if len(v) == 0 || len(v) > 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// statusWriter keeps the status the application answered, and lets a flush,
// a connection takeover and http.ResponseController through to the writer it
// wraps.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status, w.wrote = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		w.wrote = true
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("servertrace: the response writer cannot be hijacked")
	}
	return h.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
