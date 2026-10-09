package middleware

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/arandu-io/framework/observability"
)

// nPlusOneThreshold is how many identical statements in one request count as a
// suspected N+1.
const nPlusOneThreshold = 5

// Observe installs the request id, the request-scoped logger and -- in
// development, or under an authorized tracing header -- the Collector.
//
// It must come right after Recover: everything below depends on the context it
// builds.
//
// tracingSecret enables the Collector outside development for requests carrying
// it in X-Arandu-Trace. Leave it empty to keep production at zero cost.
//
// recorder is the buffer behind /_arandu/debug. Pass kernel.Recorder(); nil
// records nothing, which is what production does.
func Observe(dev bool, tracingSecret string, recorder *observability.Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			id := sanitizeRequestID(r.Header.Get("X-Request-ID"))
			if id == "" {
				id = newRequestID()
			}
			w.Header().Set("X-Request-ID", id)

			ctx := r.Context()

			log := observability.Log(ctx).With(
				"request_id", id,
				"method", r.Method,
				"path", r.URL.Path,
			)
			ctx = observability.WithLogger(ctx, log)

			// The Collector costs memory: only install it in development or
			// under the tracing secret.
			if dev || (tracingSecret != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get(observability.TracingHeader)), []byte(tracingSecret)) == 1) {
				ctx = observability.WithCollector(ctx, observability.NewCollector(id))
			}

			rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(ctx))

			duration := time.Since(start)
			status := rw.status
			var attrs []any
			if rw.hijacked {
				// The handler took the connection, so no response went through
				// rw: the 200 it was seeded with is not an answer anybody sent,
				// the byte count is not the traffic, and the time is how long the
				// connection lived rather than how long a response took. An
				// upgrade answers 101 on the raw connection, which is the status
				// it is logged under; any other takeover has no status to log.
				attrs = append(attrs, "hijacked", true)
				status = 0
				if upgradeRequested(r) {
					status = http.StatusSwitchingProtocols
					attrs = append(attrs, "status", status, "upgrade", r.Header.Get("Upgrade"))
				}
				attrs = append(attrs, "connection_ms", duration.Milliseconds())
			} else {
				attrs = append(attrs,
					"status", status,
					"duration_ms", duration.Milliseconds(),
					"bytes", rw.bytes,
				)
			}

			col := observability.FromContext(ctx)
			if col != nil {
				attrs = append(attrs, "queries", col.QueryCount(), "sql_ms", col.QueryTime().Milliseconds())

				// The warning names the statement and how many times it ran,
				// because "suspected_n_plus_one: 1" in a log line tells you a
				// problem exists and nothing about which one.
				for sql, n := range col.SuspectedNPlusOne(nPlusOneThreshold) {
					log.Warn("likely N+1",
						"statement", strings.Join(strings.Fields(sql), " "),
						"times", n,
						"console", observability.ConsolePath+"/"+id)
				}

				if rw.hijacked {
					// The record has no field to say the connection was taken,
					// so the Collector carries it, with the same keys the log
					// line has.
					col.RecordEvent(hijackedEvent, map[string]any{
						"upgrade":       r.Header.Get("Upgrade"),
						"connection_ms": duration.Milliseconds(),
					})
				}
				recorder.Record(observability.Recorded{
					RequestID: id,
					Method:    r.Method,
					Path:      r.URL.Path,
					Status:    status,
					Duration:  duration,
					At:        start,
					Collector: col,
				})
			}
			log.Info("request completed", attrs...)
		})
	}
}

// hijackedEvent is the Collector event that marks a request whose handler took
// the connection over.
const hijackedEvent = "http.hijacked"

// statusWriter records the status and the byte count for the access log, and
// whether the handler took the connection over.
type statusWriter struct {
	http.ResponseWriter
	status   int
	bytes    int
	wrote    bool
	hijacked bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush keeps server-sent events working through the wrapper. HTMX streams over
// SSE, so losing Flush here would break it in a way that is very hard to trace.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack takes the connection over through the writer underneath, and records
// that it was taken.
//
// It is a method rather than left to Unwrap because http.ResponseController
// asks the outermost writer for Hijack before it unwraps, and a takeover that
// went around this wrapper is one the access log would report as the 200 it
// was seeded with. The writer underneath is reached through a controller of
// its own, so a wrapper below this one is followed the same way. Only a
// takeover that succeeded is recorded: a refused one leaves the handler
// answering through the writer, and that answer is the status to log.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, brw, err
}

// Unwrap lets http.ResponseController reach the original writer, which is how
// deadlines keep working behind the wrapper.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// upgradeRequested reports whether r asked to switch protocols: an Upgrade
// header naming one, and a Connection header carrying the upgrade token.
//
// The Connection header is a comma-separated list that may arrive split over
// several lines, and its tokens are case-insensitive, so it is read token by
// token rather than compared whole.
func upgradeRequested(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Upgrade")) == "" {
		return false
	}
	for _, line := range r.Header.Values("Connection") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sanitizeRequestID accepts an inbound id only when it is short and hexadecimal.
// An id echoed from the client lands in every log line of the request, so
// anything else is an injection vector into the log aggregator.
func sanitizeRequestID(v string) string {
	if len(v) == 0 || len(v) > 64 {
		return ""
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		hexDigit := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !hexDigit && c != '-' {
			return ""
		}
	}
	return v
}
