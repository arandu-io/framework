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
