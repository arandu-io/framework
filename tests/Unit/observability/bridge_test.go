// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/hesape, against the
// code that now runs; the unit tests that used to live here were tests of an
// implementation this package no longer holds, and keeping a second copy of
// them would be a second place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- and one round trip
// per call through, because a rename can be wired to the wrong function and
// still compile.

package unit

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arandu-io/framework/observability"
	hlog "github.com/arandu-io/hesape/log"
)

// TestAliasesAreTheHesapeSymbols is the whole of the alias half of this bridge.
//
// Every line is a compile-time assertion that the two names are one type or one
// value. A rename in hesape that this package has not followed fails here
// rather than in the thirteen repositories that import these names.
func TestAliasesAreTheHesapeSymbols(t *testing.T) {
	var (
		_ *hlog.Collector           = (*observability.Collector)(nil)
		_ *observability.Collector  = (*hlog.Collector)(nil)
		_ hlog.QueryRecord          = observability.QueryRecord{}
		_ observability.QueryRecord = hlog.QueryRecord{}
		_ hlog.DumpRecord           = observability.DumpRecord{}
		_ hlog.EventRecord          = observability.EventRecord{}
		_ hlog.ExternalRecord       = observability.ExternalRecord{}
		_ hlog.RenderRecord         = observability.RenderRecord{}
		_ hlog.Frame                = observability.Frame{}
		_ hlog.Timeline             = observability.Timeline{}
		_ hlog.Recorded             = observability.Recorded{}
		_ *hlog.Recorder            = (*observability.Recorder)(nil)
		_ *hlog.Console             = (*observability.Console)(nil)
	)

	for name, pair := range map[string][2]any{
		"ConsolePath":         {observability.ConsolePath, hlog.ConsolePath},
		"TracingHeader":       {observability.TracingHeader, hlog.TracingHeader},
		"DefaultRecorderSize": {observability.DefaultRecorderSize, hlog.DefaultRecorderSize},
	} {
		if pair[0] != pair[1] {
			t.Errorf("observability.%s is not the hesape value: %v against %v", name, pair[0], pair[1])
		}
	}
}

// TestLoggerRenamesReachHesape covers the four renames the move made, which are
// the only place in this package a name changed.
//
// The context key is the point of the first two: a logger installed under the
// old name has to be readable under the new one, or a request that passed
// through the bridge middleware would log through slog.Default() and lose the
// configured handler without anything failing.
func TestLoggerRenamesReachHesape(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	if got := hlog.For(observability.WithLogger(context.Background(), logger)); got != logger {
		t.Error("WithLogger is not log.Into: hesape/log.For does not read back what it stored")
	}
	if got := observability.Log(hlog.Into(context.Background(), logger)); got != logger {
		t.Error("Log is not log.For: it does not read back what hesape/log.Into stored")
	}

	var seen *slog.Logger
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = observability.Log(r.Context())
	})
	observability.RootLogger(logger)(next).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if seen != logger {
		t.Error("RootLogger is not log.Middleware: the handler did not see the installed logger")
	}

	if observability.NewLogger("dev", slog.LevelInfo) == nil {
		t.Error("NewLogger is not log.New: it answered nil")
	}
}

// TestCollectorIsOneType proves the alias is worth more than a saved import:
// a Collector installed through this package is the one hesape records onto,
// which is what lets the data layer, the console and the error page disagree
// about which name to spell and still share a request.
func TestCollectorIsOneType(t *testing.T) {
	col := observability.NewCollector("req-1")

	ctx := observability.WithCollector(context.Background(), col)
	hlog.Dump(ctx, "from-hesape", 1)

	if got := hlog.FromContext(ctx); got != col {
		t.Fatal("WithCollector does not install where hesape/log.FromContext reads")
	}

	observability.Dump(hlog.WithCollector(context.Background(), col), "from-bridge", 2)

	if got := len(col.Dumps()); got != 2 {
		t.Fatalf("both names should record on the one Collector: got %d dumps, want 2", got)
	}
}

// TestCollectorSlotIsShared covers the reservation the Recover middleware makes
// before the Collector exists. A slot from one name and a fill from the other
// have to be the same slot, or the error page draws a stack and nothing else.
func TestCollectorSlotIsShared(t *testing.T) {
	ctx := observability.WithCollectorSlot(context.Background())
	col := observability.NewCollector("req-2")
	hlog.WithCollector(ctx, col)

	if got := observability.FromContext(ctx); got != col {
		t.Error("the slot reserved here is not the slot hesape fills")
	}
}

// TestDumpDieSentinelIsHesapes is the assertion that would have been silent.
//
// The sentinel is an unexported type on both sides, so a second copy of it
// would compile, panic, and be recognised by nobody: the middleware would treat
// a dd() as a real 500 and draw the error page instead of the dump page.
func TestDumpDieSentinelIsHesapes(t *testing.T) {
	func() {
		defer func() {
			v := recover()
			if v == nil {
				t.Fatal("DumpDie did not abort")
			}
			if !hlog.IsDumpDie(v) {
				t.Error("DumpDie panics with a sentinel hesape/log.IsDumpDie does not recognise")
			}
		}()
		observability.DumpDie(context.Background(), "label", "value")
	}()

	func() {
		defer func() {
			if v := recover(); !observability.IsDumpDie(v) {
				t.Error("IsDumpDie does not recognise the sentinel hesape/log.DumpDie panics with")
			}
		}()
		hlog.DumpDie(context.Background(), "label", "value")
	}()
}

// TestEditorLinkReachesHesape covers the one signature that lost something: the
// optional path rewrite has no place in this one, so what is left to prove is
// that the table answering is hesape's.
func TestEditorLinkReachesHesape(t *testing.T) {
	for _, editor := range []string{"zed", "goland", "cursor", "vscode", "emacs", ""} {
		want := hlog.EditorLink(editor, "/src/app/main.go", 12)
		if got := observability.EditorLink(editor, "/src/app/main.go", 12); got != want {
			t.Errorf("EditorLink(%q) = %q, hesape answers %q", editor, got, want)
		}
	}
}

// TestConstructorsAnswer covers the remaining call-throughs, which take and
// return aliased types and so have nowhere to go wrong except by calling the
// wrong function.
func TestConstructorsAnswer(t *testing.T) {
	rec := observability.NewRecorder(2)
	rec.Record(observability.Recorded{RequestID: "req-3"})
	if _, ok := rec.Find("req-3"); !ok {
		t.Error("NewRecorder did not answer a recorder that records")
	}

	if observability.NewConsole(rec, "vscode", observability.NewGauges()) == nil {
		t.Error("NewConsole answered nil")
	}

	if observability.NewConsole(rec, "vscode", nil) == nil {
		t.Error("NewConsole answered nil for a caller with no gauges")
	}

	if got := observability.Client(3 * time.Second).Timeout; got != 3*time.Second {
		t.Errorf("Client kept the timeout as %v, want 3s", got)
	}

	if observability.Transport(nil) == nil {
		t.Error("Transport answered nil for the default transport")
	}
}
