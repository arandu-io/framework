package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
			attrs := []any{
				"status", rw.status,
				"duration_ms", duration.Milliseconds(),
				"bytes", rw.bytes,
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

				recorder.Record(observability.Recorded{
					RequestID: id,
					Method:    r.Method,
					Path:      r.URL.Path,
					Status:    rw.status,
					Duration:  duration,
					At:        start,
					Collector: col,
				})
			}
			log.Info("request completed", attrs...)
		})
	}
}

// statusWriter records the status and the byte count for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
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

// Unwrap lets http.ResponseController reach the original writer, which is how
// deadlines and hijacking keep working behind the wrapper.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
