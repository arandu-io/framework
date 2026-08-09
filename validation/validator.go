// Package validation defines the validation contract.
//
// There is no reflection and there are no struct tags: every request type
// implements Validate and returns the errors per field. It is more verbose than
// `binding:"required"`, and deliberately so -- the message is written by whoever
// knows the domain, and the CLI generates the method skeleton along with the
// struct.
package validation

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Errors maps a field to its messages. It serializes straight into the HTMX
// partial that re-renders the form with inline errors.
type Errors map[string][]string

// Add appends a message to a field. It is a no-op on a nil map, so a caller
// that forgot to initialize the map does not panic in the middle of a request.
func (e Errors) Add(field, msg string) {
	if e == nil {
		return
	}
	e[field] = append(e[field], msg)
}

// Any reports whether validation failed.
func (e Errors) Any() bool { return len(e) > 0 }

// Error renders the errors with fields in a stable order, so that logs and
// golden files do not change between runs.
func (e Errors) Error() string {
	fields := make([]string, 0, len(e))
	for f := range e {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	var b strings.Builder
	b.WriteString("validation failed: ")
	for i, f := range fields {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s (%s)", f, strings.Join(e[f], ", "))
	}
	return b.String()
}

// Validatable is implemented by every request type.
type Validatable interface {
	Validate() Errors
}

// The helper list below is short on purpose: domain rules do not belong to the
// framework.

// Required rejects an empty or blank value.
func Required(e Errors, field, value string) {
	if strings.TrimSpace(value) == "" {
		e.Add(field, "is required")
	}
}

// MinLen counts runes, not bytes: a limit measured in bytes rejects valid input
// in any language that needs more than one byte per character.
func MinLen(e Errors, field, value string, n int) {
	if len([]rune(value)) < n {
		e.Add(field, fmt.Sprintf("must be at least %d characters", n))
	}
}

// MaxLen counts runes, not bytes.
func MaxLen(e Errors, field, value string, n int) {
	if len([]rune(value)) > n {
		e.Add(field, fmt.Sprintf("must be at most %d characters", n))
	}
}

// Email checks the shape only. Deliverability is proven by sending mail, never
// by a regular expression.
//
// Whitespace is rejected rather than trimmed: an address with a space in it is
// almost always a paste accident, and silently trimming input hides the mistake
// from the person who made it.
func Email(e Errors, field, value string) {
	if strings.ContainsAny(value, " \t\r\n") {
		e.Add(field, "is not a valid email address")
		return
	}
	at := strings.IndexByte(value, '@')
	if at <= 0 || at == len(value)-1 || !strings.Contains(value[at:], ".") {
		e.Add(field, "is not a valid email address")
	}
}

// NotZero reports a value that was never filled in.
//
// It is Required for everything that is not text. Required takes a string and
// the generator used to hand it a literal "" for an int, a date or an amount --
// so every required field of those types failed validation with "is required"
// no matter what was sent, and the generated create endpoint could not be used
// at all. Found by audit.
//
// A time.Time is asked rather than compared: a parsed "0001-01-01T00:00:00Z"
// carries a location the zero value does not, so == says they differ when they
// do not.
//
// Bool has no meaningful zero to reject -- false is an answer, not an absence --
// and the specification refuses `required` on a bool for that reason.
func NotZero[T comparable](e Errors, field string, value T) {
	if t, ok := any(value).(time.Time); ok {
		if t.IsZero() {
			e.Add(field, "is required")
		}
		return
	}
	var zero T
	if value == zero {
		e.Add(field, "is required")
	}
}

// Confirmed rejects a value its confirmation field does not repeat.
//
// It is what a "confirm your password" box is for, and the message goes on the
// confirmation rather than on the field itself: a form that reports "password
// does not match" next to the first box tells the person to change the one they
// meant, and they change it, and the form fails again.
//
// An empty confirmation is reported here rather than by Required, so the field
// gets one message instead of two saying the same thing.
func Confirmed(e Errors, field, value, confirmation string) {
	if value == "" {
		// Nothing to confirm yet. Whatever rule rejected the value itself has
		// already said so, and a second message about the copy is noise.
		return
	}
	if confirmation == "" {
		e.Add(field, "is required")
		return
	}
	if value != confirmation {
		e.Add(field, "does not match")
	}
}

// First returns one message, for a view that shows a single line.
//
// A form that lists every error next to its field uses the map directly. One
// that shows a banner needs one sentence, and picking it at the call site means
// every template does it slightly differently.
//
// The field order is not stable -- a map has none -- so this is for the case
// where any of them answers the question "what is wrong". When which field
// failed matters, read the map.
func (e Errors) First() string {
	for _, msgs := range e {
		if len(msgs) > 0 {
			return msgs[0]
		}
	}
	return ""
}
