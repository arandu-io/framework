// The logger, answered by github.com/arandu-io/hesape/log.
//
// All four names were renamed on the way over, so all four are a call through
// rather than an alias. The types they carry are the standard library's, so
// nothing about the values changed: a *slog.Logger built here is the same
// *slog.Logger hesape builds.

package observability

import (
	"context"
	"log/slog"
	"net/http"

	hlog "github.com/arandu-io/hesape/log"
)

// NewLogger returns the root logger: readable text in development, JSON
// everywhere else, so it reaches the aggregator without fragile parsing.
//
// It is hesape/log.New. The rename is the only difference; hesape also renders
// the four PSR-3 levels slog does not name, which a caller sees only in the
// output.
func NewLogger(env string, level slog.Level) *slog.Logger {
	return hlog.New(env, level)
}

// WithLogger stores the request-scoped logger in the context.
//
// It is hesape/log.Into. The context key belongs to hesape, so a logger put in
// by this name is read back by hesape/log.For and the other way round.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return hlog.Into(ctx, l)
}

// RootLogger installs the application logger at the very top of the pipeline.
//
// Without it, Log(ctx) inside a request falls back to slog.Default(), which
// ignores the configured handler: production would emit its request lines in the
// default text format instead of the JSON the aggregator expects, and the level
// filter from the configuration would not apply either. The Kernel installs this
// as the outermost middleware, so even a panic in Recover logs correctly.
//
// It is hesape/log.Middleware, which returns the same func(http.Handler)
// http.Handler this has always returned.
func RootLogger(l *slog.Logger) func(http.Handler) http.Handler {
	return hlog.Middleware(l)
}

// Log returns the request logger. It never returns nil.
//
// This is the only way to log inside a handler or a service. There is no
// exported global logger, on purpose: a log line without request_id and tenant
// is noise, and the only way to guarantee both is to force the context through.
//
// It is hesape/log.For.
func Log(ctx context.Context) *slog.Logger {
	return hlog.For(ctx)
}
