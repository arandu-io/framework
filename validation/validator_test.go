package validation_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/framework/validation"
)

func TestRequired(t *testing.T) {
	e := validation.Errors{}
	validation.Required(e, "name", "  ")
	validation.Required(e, "email", "someone@example.com")

	if len(e["name"]) != 1 {
		t.Errorf("a blank value must fail: %v", e)
	}
	if len(e["email"]) != 0 {
		t.Errorf("a filled value must pass: %v", e)
	}
}

// TestLengthCountsRunes: a limit measured in bytes rejects valid input in every
// language that needs more than one byte per character.
func TestLengthCountsRunes(t *testing.T) {
	e := validation.Errors{}
	validation.MinLen(e, "name", "José", 4)
	validation.MaxLen(e, "city", "São Paulo", 9)

	if e.Any() {
		t.Fatalf("accented input was rejected by a byte based limit: %v", e)
	}
}

func TestMinAndMaxLen(t *testing.T) {
	e := validation.Errors{}
	validation.MinLen(e, "short", "ab", 3)
	validation.MaxLen(e, "long", "abcd", 3)

	if len(e["short"]) != 1 || len(e["long"]) != 1 {
		t.Fatalf("errors = %v, want one per field", e)
	}
	if !strings.Contains(e["short"][0], "at least 3") {
		t.Errorf("the message must state the limit, got %q", e["short"][0])
	}
}

func TestEmail(t *testing.T) {
	valid := []string{"a@b.co", "someone.else@sub.example.com"}
	invalid := []string{"", "a", "@b.co", "a@", "a@b", "a b@c.co "}

	for _, v := range valid {
		e := validation.Errors{}
		validation.Email(e, "email", v)
		if e.Any() {
			t.Errorf("%q was rejected", v)
		}
	}
	for _, v := range invalid {
		e := validation.Errors{}
		validation.Email(e, "email", v)
		if !e.Any() {
			t.Errorf("%q was accepted", v)
		}
	}
}

func TestAddOnNilMapDoesNotPanic(t *testing.T) {
	var e validation.Errors

	e.Add("field", "message") // a caller that forgot to initialize must not crash a request

	if e.Any() {
		t.Fatal("a nil map must stay empty")
	}
}

// TestErrorMessageIsStable: fields come out sorted, so logs and golden files do
// not change between runs of the same input.
func TestErrorMessageIsStable(t *testing.T) {
	e := validation.Errors{}
	e.Add("zeta", "last")
	e.Add("alpha", "first")
	e.Add("alpha", "also first")

	got := e.Error()
	want := "validation failed: alpha (first, also first); zeta (last)"
	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	for range 20 {
		if e.Error() != want {
			t.Fatal("Error() is not deterministic across calls")
		}
	}
}

// TestErrorsSatisfiesError lets a service return validation failures as a plain
// error, which is what keeps handlers free of type switches.
func TestErrorsSatisfiesError(t *testing.T) {
	var err error = validation.Errors{"email": {"is required"}}

	if !strings.Contains(err.Error(), "email") {
		t.Fatalf("Error() = %q", err.Error())
	}
}
