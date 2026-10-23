package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// RedactionMarker is what a redacted value is written as.
const RedactionMarker = "[redacted]"

// Redacted is one value Redaction checks: a request an authentication screen
// binds, what it must never show, and what it must still show.
type Redacted struct {
	// Name names the case in the test output.
	Name string
	// Value is the request, filled with the values below.
	Value any
	// Secrets must appear in no rendering: the password, its confirmation, a
	// document number, a one-time code -- each in every spelling the value
	// could carry it in.
	Secrets []string
	// Keeps must appear in every rendering: what a person reading the log
	// needs to tell what was sent -- the address, the name, how many digits
	// were typed.
	Keeps []string
	// Markers is how many times RedactionMarker appears in each rendering.
	// Zero does not count them.
	Markers int
	// LogOnly checks the log line alone, for a value nothing renders as
	// JSON.
	LogOnly bool
}

// Redaction holds each value to what a structured log line and its JSON
// rendering may carry. A request is the first thing anybody logs while working
// out why a form refused something, so the guard is on the type: a value that
// carries a secret into either rendering fails, and so does one redacted so
// hard the line no longer says what was sent.
func Redaction(t *testing.T, cases ...Redacted) {
	t.Helper()
	if len(cases) == 0 {
		t.Fatal("conformance.Redaction needs at least one value to check")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			for _, problem := range redactionProblems(c) {
				t.Error(problem)
			}
		})
	}
}

// redactionProblems renders the value as a log line and as JSON and reports
// what either carries that it must not, or lacks that it must.
func redactionProblems(c Redacted) []string {
	var line bytes.Buffer
	slog.New(slog.NewTextHandler(&line, nil)).Info("request", "input", c.Value)
	renderings := []struct{ name, text string }{{"the log line", line.String()}}
	if !c.LogOnly {
		encoded, err := json.Marshal(c.Value)
		if err != nil {
			return []string{"marshalling the value: " + err.Error()}
		}
		renderings = append(renderings, struct{ name, text string }{"the JSON rendering", string(encoded)})
	}
	var problems []string
	for _, r := range renderings {
		for _, secret := range c.Secrets {
			if secret != "" && strings.Contains(r.text, secret) {
				problems = append(problems, fmt.Sprintf("%s carries %q: %s", r.name, secret, r.text))
			}
		}
		for _, kept := range c.Keeps {
			if !strings.Contains(r.text, kept) {
				problems = append(problems, fmt.Sprintf("%s lacks %q, which it is read for: %s", r.name, kept, r.text))
			}
		}
		if c.Markers > 0 {
			if got := strings.Count(r.text, RedactionMarker); got != c.Markers {
				problems = append(problems, fmt.Sprintf("%s has %d redaction markers, want %d: %s", r.name, got, c.Markers, r.text))
			}
		}
	}
	return problems
}
