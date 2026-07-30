package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
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
func Observe(dev bool, tracingSecret string) func(http.Handler) http.Handler {
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
			if dev || (tracingSecret != "" && r.Header.Get("X-Arandu-Trace") == tracingSecret) {
				ctx = observability.WithCollector(ctx, observability.NewCollector(id))
			}

			rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(ctx))

			attrs := []any{
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", rw.bytes,
			}
			if col := observability.FromContext(ctx); col != nil {
				attrs = append(attrs, "queries", len(col.Queries), "sql_ms", col.QueryTime().Milliseconds())
				if n := col.SuspectedNPlusOne(nPlusOneThreshold); len(n) > 0 {
					attrs = append(attrs, "suspected_n_plus_one", len(n))
				}
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
