package observability

import (
	"context"
	"log/slog"
	"net/http"
	"os"
)

type ctxLoggerKey struct{}

// NewLogger returns the root logger: readable text in development, JSON
// everywhere else, so it reaches the aggregator without fragile parsing.
func NewLogger(env string, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, AddSource: true}
	if env == "dev" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// WithLogger stores the request-scoped logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxLoggerKey{}, l)
}

// RootLogger installs the application logger at the very top of the pipeline.
//
// Without it, Log(ctx) inside a request falls back to slog.Default(), which
// ignores the configured handler: production would emit its request lines in the
// default text format instead of the JSON the aggregator expects, and the level
// filter from the configuration would not apply either. The Kernel installs this
// as the outermost middleware, so even a panic in Recover logs correctly.
func RootLogger(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithLogger(r.Context(), l)))
		})
	}
}

// Log returns the request logger. It never returns nil.
//
// This is the only way to log inside a handler or a service. There is no
// exported global logger, on purpose: a log line without request_id and tenant
// is noise, and the only way to guarantee both is to force the context through.
func Log(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxLoggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
