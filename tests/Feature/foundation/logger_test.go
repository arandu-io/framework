package feature

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
)

// loggedBy composes an application over env, writes an info, a debug and a
// warning line through its root logger, and answers what reached the
// standard output.
func loggedBy(t *testing.T, env ...string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	app, err := configured(t, nil, env...)
	os.Stdout = stdout
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	app.Logger().Info("invoice sent", "invoice", "inv_1")
	app.Logger().Debug("invoice rendered", "invoice", "inv_1")
	app.Logger().Warn("invoice overdue", "invoice", "inv_1")
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the standard output: %v", err)
	}
	return string(out)
}

// TestTheRootLoggerIsTheLoggingSections: an application that sets nothing
// logs to the standard output as it did -- readable text in development, from
// info up -- and LOG_FORMAT and LOG_LEVEL reach the logger.
func TestTheRootLoggerIsTheLoggingSections(t *testing.T) {
	t.Run("nothing set", func(t *testing.T) {
		out := loggedBy(t)
		if !strings.Contains(out, "level=INFO") || !strings.Contains(out, `msg="invoice sent"`) || !strings.Contains(out, "invoice=inv_1") {
			t.Errorf("an application that sets nothing logged %q, want the info line as text on the standard output", out)
		}
		if strings.Contains(out, "invoice rendered") {
			t.Errorf("the debug line was written under the default level: %q", out)
		}
	})

	t.Run("LOG_FORMAT=json", func(t *testing.T) {
		out, _, _ := strings.Cut(loggedBy(t, "LOG_FORMAT", "json"), "\n")
		var line map[string]any
		if err := json.Unmarshal([]byte(out), &line); err != nil {
			t.Fatalf("LOG_FORMAT=json logged %q, which is not one JSON line: %v", out, err)
		}
		if line["level"] != "INFO" || line["msg"] != "invoice sent" || line["invoice"] != "inv_1" {
			t.Errorf("LOG_FORMAT=json logged %v", line)
		}
	})

	t.Run("LOG_LEVEL=warning", func(t *testing.T) {
		out := loggedBy(t, "LOG_LEVEL", "warning")
		if strings.Contains(out, "invoice sent") || !strings.Contains(out, "invoice overdue") {
			t.Errorf("LOG_LEVEL=warning logged %q, want the warning line and not the info one", out)
		}
	})
}

// TestALevelWithNoNameIsRefused: a logging section built by hand with a level
// none of the eight names spells stops Create, rather than logging under a
// level nobody asked for.
func TestALevelWithNoNameIsRefused(t *testing.T) {
	s := settingsFor(t)
	s.Logging.Level = slog.LevelInfo + 1
	d, err := foundation.NewDriversOver(s, pingDB(t))
	if err != nil {
		t.Fatalf("NewDriversOver: %v", err)
	}
	if _, err := foundation.Configure(d).Create(); err == nil || !strings.Contains(err.Error(), "log level") {
		t.Errorf("an unnamed level answered %v, want the refusal", err)
	}
}
