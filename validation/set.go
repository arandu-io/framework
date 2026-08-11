package validation

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Set is a compiled, checked rule set.
//
// Build one in a package-level variable with MustCompile: the rules are then
// parsed once, at boot, and a set that boots is a set whose names are all real.
// A Set is read-only once compiled, so one is shared by every request.
type Set struct {
	fields   []*field // sorted by name, so failures come out in a stable order
	byName   map[string]*field
	messages Messages
	file     string
	line     int
}

// field is one input and the rules written against it, in the order written.
type field struct {
	name  string
	rules []*rule

	// bail stops the field at its first failure.
	bail bool
	// sometimes skips the field entirely when its key was not sent at all --
	// which is why the input is url.Values and not a map of strings: only
	// url.Values can tell "absent" from "present and empty", and that is the
	// difference a PATCH is made of.
	sometimes bool
	// numeric is set by numeric, integer or decimal, and makes every size rule
	// on this field measure the value rather than the characters.
	numeric bool
	// layout is the date_format layout, which the date comparisons read.
	layout string
}

// rule is one rule of one field, with whatever its boot check parsed for it.
type rule struct {
	name string
	args []string
	spec *spec

	// re is the compiled pattern of regex/not_regex, and nums the parsed
	// numeric arguments. Both are filled at boot, so a request parses nothing.
	re   *regexp.Regexp
	nums []float64
}

func (f *field) rule(name string) *rule {
	for _, r := range f.rules {
		if r.name == name {
			return r
		}
	}
	return nil
}

// Fields returns the input names this set declares, sorted.
func (s *Set) Fields() []string {
	names := make([]string, len(s.fields))
	for i, f := range s.fields {
		names[i] = f.name
	}
	return names
}

// RulesFor returns the rule names written against one field, in the order they
// were written. It is nil for a field the set does not declare.
func (s *Set) RulesFor(field string) []string {
	f, ok := s.byName[field]
	if !ok {
		return nil
	}
	names := make([]string, len(f.rules))
	for i, r := range f.rules {
		names[i] = r.name
	}
	return names
}

// Source returns where the set was compiled, which is what a boot failure
// names.
func (s *Set) Source() (file string, line int) { return s.file, s.line }

// Validate runs the set over a submitted form and returns what passed and what
// failed.
//
// The input is url.Values rather than a map of strings because "absent" and
// "present and empty" are different questions, and `sometimes` asks the first
// one. A rule reads the first value of a field; a multi-value input is read out
// with Input.Strings.
//
// The returned Errors is nil when nothing failed. Assigning it to an error
// interface makes that interface non-nil even so, because the type is not nil
// -- callers ask Any(), and httpx.Context.Validate returns a plain nil for
// exactly this reason.
func (s *Set) Validate(values url.Values) (Input, Errors) {
	var errs Errors
	passed := make(url.Values, len(s.fields))

	for _, f := range s.fields {
		_, present := values[f.name]
		if f.sometimes && !present {
			// Not sent at all, and the set says that is allowed. Nothing else
			// on this field has an opinion about a value that is not there.
			continue
		}

		e := &eval{set: s, f: f, values: values, value: values.Get(f.name)}
		failed := false
		for _, r := range f.rules {
			// A rule that is not implicit does not run on a blank value.
			// Without this, `min:12` reports "must be at least 12 characters"
			// about an optional box nobody typed in. Laravel's
			// presentOrRuleIsImplicit.
			if !r.spec.implicit && !filled(e.value) {
				continue
			}
			if r.spec.eval(e, r) {
				continue
			}
			if errs == nil {
				errs = Errors{}
			}
			errs.Add(f.name, s.message(f, r))
			failed = true

			// A failed implicit rule stops the field: `required` failing must
			// not also produce "must be at least 12 characters" about the same
			// empty box. Laravel's shouldStopValidating, and `bail` is the same
			// stop asked for explicitly.
			if f.bail || r.spec.implicit {
				break
			}
		}
		if !failed && present {
			passed[f.name] = slices.Clone(values[f.name])
		}
	}
	return Input{values: passed}, errs
}

// message is the sentence one failure puts on the field, after any override.
func (s *Set) message(f *field, r *rule) string {
	if custom, ok := s.messages[f.name+"."+r.name]; ok {
		return custom
	}
	return r.spec.message(f, r)
}

// eval is one field's turn: the value under test and the form it came in.
type eval struct {
	set    *Set
	f      *field
	values url.Values
	value  string
}

func (e *eval) has(name string) bool {
	_, ok := e.values[name]
	return ok
}

func (e *eval) other(name string) string { return e.values.Get(name) }

// size is what min, max, size, between and the four comparisons measure.
//
// It is polymorphic exactly as Laravel's getSize is: a string is measured in
// RUNES, never bytes -- a limit in bytes rejects valid input in every language
// that needs more than one byte per character -- and a field that declares
// numeric, integer or decimal is measured by its VALUE. Both halves of the gate
// matter: a field that declares `integer` and was sent "abc" is measured as
// three characters, because there is no number to measure.
func (e *eval) size(v string) float64 {
	if e.f.numeric {
		if n, ok := number(v); ok {
			return n
		}
	}
	return float64(len([]rune(v)))
}

// Input is what passed the rules. It is the only way to read a submitted value
// out of a validated request: a field the set does not declare is not in here,
// so a value nobody wrote a rule for cannot reach a repository by accident.
type Input struct {
	values url.Values
}

// Has reports whether the field was sent and passed.
func (in Input) Has(field string) bool {
	_, ok := in.values[field]
	return ok
}

// String returns the value, or empty when the field was not sent.
func (in Input) String(field string) string { return in.values.Get(field) }

// Strings returns every value of a field, which is what a multi-select sends.
func (in Input) Strings(field string) []string { return slices.Clone(in.values[field]) }

// Int returns the value as a whole number. It is zero when the field was not
// sent or does not hold one -- which is a rule's job to have proven, with
// `integer`.
func (in Input) Int(field string) int64 {
	n, _ := whole(in.values.Get(field))
	return n
}

// Float returns the value as a number, zero when there is none. `numeric` or
// `decimal` is what proves there is.
func (in Input) Float(field string) float64 {
	n, _ := number(in.values.Get(field))
	return n
}

// Bool reads a checkbox.
//
// It accepts every value `accepted` does -- "1", "on", "yes", "true" -- and not
// only the "0" and "1" that the `boolean` rule allows, because an unticked
// checkbox sends nothing and a ticked one sends "on". Anything else is false: a
// checkbox has no third answer.
func (in Input) Bool(field string) bool { return contains(acceptable, in.values.Get(field)) }

// Time reads a value with the layout its rule declared. It is the zero time
// when the field was not sent or does not parse, which `date_format` is what
// prevents.
func (in Input) Time(field, layout string) time.Time {
	t, err := time.Parse(layout, in.values.Get(field))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Values returns a copy of everything that passed, for a caller that hands the
// whole form on. It is a copy so that a caller cannot reach back into the
// request's own values through it.
func (in Input) Values() url.Values {
	out := make(url.Values, len(in.values))
	for k, v := range in.values {
		out[k] = slices.Clone(v)
	}
	return out
}

// Humanize turns a form field name into what a sentence calls it:
// "password_confirmation" becomes "Password confirmation".
//
// It is exported because the messages in this package are written to be drawn
// WITHOUT a field name -- components.Field puts "must be at least 12
// characters" under a labelled input -- and a banner needs one in front:
// view.Page.ErrorSummary renders "Password must be at least 12 characters".
// Two implementations of that would disagree on the first line somebody read.
func Humanize(field string) string {
	s := strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(field))
	if s == "" {
		return field
	}
	r := []rune(s)
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}
