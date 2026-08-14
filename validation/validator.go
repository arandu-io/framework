// The failures, the request contract and the one-value helpers, answered by
// github.com/arandu-io/hesape/validation -- and Humanize, answered by
// github.com/arandu-io/hesape/str, because naming a field the way a sentence
// does is a string question and not a validation one.

package validation

import (
	"github.com/arandu-io/hesape/str"
	hvalidation "github.com/arandu-io/hesape/validation"
)

// Errors maps a field to its messages. It serializes straight into the HTMX
// partial that re-renders the form with inline errors.
//
// The alias carries the whole message bag with it: the four methods this
// package declared -- Add, Any, Error and First -- are still there, alongside
// the rest of Illuminate's MessageBag. First took no argument here and takes an
// optional key there, so e.First() reads as it always did and e.First("email")
// is new.
type Errors = hvalidation.Errors

// Validatable is implemented by every request type.
type Validatable = hvalidation.Validatable

// The helper list below is short on purpose: domain rules do not belong to the
// framework. Each one is a plain function, which has no alias form in Go, so
// each is a call through.

// Required rejects an empty or blank value.
func Required(e Errors, field, value string) { hvalidation.Required(e, field, value) }

// MinLen counts runes, not bytes: a limit measured in bytes rejects valid input
// in any language that needs more than one byte per character.
func MinLen(e Errors, field, value string, n int) { hvalidation.MinLen(e, field, value, n) }

// MaxLen counts runes, not bytes.
func MaxLen(e Errors, field, value string, n int) { hvalidation.MaxLen(e, field, value, n) }

// Email checks the shape only. Deliverability is proven by sending mail, never
// by a regular expression.
//
// Whitespace is rejected rather than trimmed: an address with a space in it is
// almost always a paste accident, and silently trimming input hides the mistake
// from the person who made it.
func Email(e Errors, field, value string) { hvalidation.Email(e, field, value) }

// NotZero reports a value that was never filled in.
//
// It is Required for everything that is not text. A time.Time is asked rather
// than compared: a parsed "0001-01-01T00:00:00Z" carries a location the zero
// value does not, so == says they differ when they do not.
//
// Bool has no meaningful zero to reject -- false is an answer, not an absence.
//
// A wrapper and not an alias: Go has no alias form for a generic function.
func NotZero[T comparable](e Errors, field string, value T) {
	hvalidation.NotZero(e, field, value)
}

// Confirmed rejects a value its confirmation field does not repeat.
//
// It is what a "confirm your password" box is for, and the message goes on the
// confirmation rather than on the field itself: a form that reports "password
// does not match" next to the first box tells the person to change the one they
// meant, and they change it, and the form fails again.
func Confirmed(e Errors, field, value, confirmation string) {
	hvalidation.Confirmed(e, field, value, confirmation)
}

// Humanize turns a form field name into what a sentence calls it:
// "password_confirmation" becomes "Password Confirmation".
//
// It is exported because the messages in this package are written to be drawn
// WITHOUT a field name -- components.Field puts "must be at least 12
// characters" under a labelled input -- and a banner needs one in front:
// view.Page.ErrorSummary renders "Password must be at least 12 characters".
//
// It answers to str.Headline, which is Str::headline, and that CHANGES what it
// returns: "password_confirmation" was "Password confirmation" here and is
// "Password Confirmation" there. A one-word field reads identically, which is
// most of them. The sentence casing was an Arandu divergence from Laravel and
// hesape closed it deliberately; this is the one behaviour the move changes,
// and it changes the text of an error summary, never whether a form passes.
func Humanize(field string) string { return str.Headline(field) }
