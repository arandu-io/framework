package conformance

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// signIn redacts its password from a log line and from JSON, and keeps the
// address.
type signIn struct {
	Email    string
	Password string
}

func (r signIn) LogValue() slog.Value {
	return slog.GroupValue(slog.String("email", r.Email), slog.String("password", RedactionMarker))
}

func (r signIn) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"email": r.Email, "password": RedactionMarker})
}

// leaky redacts its password from the log line and forgets JSON.
type leaky struct {
	Email    string
	Password string
}

func (r leaky) LogValue() slog.Value {
	return slog.GroupValue(slog.String("email", r.Email), slog.String("password", RedactionMarker))
}

// identify says only how many digits were typed, in the log line, and is
// never rendered as JSON.
type identify struct{ CPF string }

func (r identify) LogValue() slog.Value {
	digits := 0
	for _, c := range r.CPF {
		if c >= '0' && c <= '9' {
			digits++
		}
	}
	return slog.GroupValue(slog.Int("cpf_digits", digits))
}

func TestARequestThatRedactsItsSecretsPasses(t *testing.T) {
	Redaction(t,
		Redacted{
			Name: "sign in", Value: signIn{Email: "ana@example.test", Password: "login-secret-9ff2"},
			Secrets: []string{"login-secret-9ff2"}, Keeps: []string{"ana@example.test"}, Markers: 1,
		},
		Redacted{
			Name: "identify", Value: identify{CPF: "529.982.247-25"}, LogOnly: true,
			Secrets: []string{"529.982.247-25", "52998224725"}, Keeps: []string{"cpf_digits=11"},
		},
	)
}

func TestWhatARenderingCarriesOrLacksIsNamed(t *testing.T) {
	for name, c := range map[string]struct {
		value Redacted
		want  string
	}{
		"a secret in the JSON": {
			Redacted{Value: leaky{Email: "ana@example.test", Password: "login-secret-9ff2"}, Secrets: []string{"login-secret-9ff2"}},
			`the JSON rendering carries "login-secret-9ff2"`,
		},
		"redacted past use": {
			Redacted{Value: identify{CPF: "1234"}, LogOnly: true, Keeps: []string{"cpf_digits=4", "captcha=false"}},
			`the log line lacks "captcha=false", which it is read for`,
		},
		"a marker short": {
			Redacted{Value: signIn{Email: "ana@example.test", Password: "x"}, Markers: 2},
			"the log line has 1 redaction markers, want 2",
		},
	} {
		t.Run(name, func(t *testing.T) {
			problems := redactionProblems(c.value)
			found := false
			for _, p := range problems {
				found = found || strings.Contains(p, c.want)
			}
			if !found {
				t.Fatalf("redactionProblems = %q, want one saying %q", problems, c.want)
			}
		})
	}
}

func init() {
	broken["Redaction/a secret reaches the JSON"] = brokenCase{
		run: func(t *testing.T) {
			Redaction(t, Redacted{Name: "login", Value: leaky{Email: "ana@example.test", Password: "login-secret-9ff2"}, Secrets: []string{"login-secret-9ff2"}})
		},
		want: []string{"--- FAIL: TestTheBrokenApplicationRunsInAChild/login", `the JSON rendering carries "login-secret-9ff2"`},
	}
	broken["Redaction/nothing to check"] = brokenCase{
		run:  func(t *testing.T) { Redaction(t) },
		want: []string{"conformance.Redaction needs at least one value to check"},
	}
}
