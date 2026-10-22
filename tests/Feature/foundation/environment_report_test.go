package feature

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// TestBootReportsTheEnvironment: the first line Boot writes says which
// environment the process came up in, with the debug flag and the address,
// so a deployment that fell back to development says so in its own log.
func TestBootReportsTheEnvironment(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	app, err := configured(t, nil, "APP_ENV", "prod", "APP_URL", "https://x.example", "HTTP_ADDR", ":9090",
		"MAIL_URL", "array://", "LOG_FORMAT", "json")
	if err == nil {
		err = app.Boot(context.Background())
		t.Cleanup(func() { _ = app.Shutdown() })
	}
	os.Stdout = stdout
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if readErr != nil {
		t.Fatalf("reading the standard output: %v", readErr)
	}

	for _, line := range strings.Split(string(out), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil || entry["msg"] != "application environment" {
			continue
		}
		if entry["env"] != "prod" || entry["debug"] != false || entry["http_addr"] != ":9090" {
			t.Errorf("Boot reported %v, want env=prod debug=false http_addr=:9090", entry)
		}
		return
	}
	t.Fatalf("Boot wrote no line naming the environment:\n%s", out)
}
