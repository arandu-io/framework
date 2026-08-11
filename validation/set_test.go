package validation_test

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/arandu-io/framework/validation"
)

// TestANonImplicitRuleDoesNotRunOnABlankValue is Laravel's
// presentOrRuleIsImplicit. Dropping it means "must be at least 12 characters"
// under an optional box nobody typed in.
func TestANonImplicitRuleDoesNotRunOnABlankValue(t *testing.T) {
	set := mustCompile(t, validation.Rules{"nickname": "min:12|alpha|email"})

	if _, errs := set.Validate(url.Values{"nickname": {""}}); errs.Any() {
		t.Errorf("an empty optional field was rejected: %v", errs)
	}
	if _, errs := set.Validate(url.Values{"nickname": {"   "}}); errs.Any() {
		t.Errorf("a blank optional field was rejected: %v", errs)
	}
	if _, errs := set.Validate(url.Values{}); errs.Any() {
		t.Errorf("an absent optional field was rejected: %v", errs)
	}
	if _, errs := set.Validate(url.Values{"nickname": {"short"}}); !errs.Any() {
		t.Error("a filled optional field skipped its rules")
	}
}

// TestAFailedRequiredStopsTheFieldsOtherMessages is Laravel's
// shouldStopValidating. An empty password box told it is required and also too
// short has been told the same thing twice.
func TestAFailedRequiredStopsTheFieldsOtherMessages(t *testing.T) {
	set := mustCompile(t, validation.Rules{"password": "required|min:12"})

	_, errs := set.Validate(url.Values{"password": {""}})
	if got := errs["password"]; len(got) != 1 || got[0] != "is required" {
		t.Fatalf("password = %v, want only \"is required\"", got)
	}
}

// TestSometimesDistinguishesAbsentFromPresentAndEmpty is why the input is
// url.Values and not a map of strings: a map cannot tell the two apart, and
// that difference is what a PATCH is made of.
func TestSometimesDistinguishesAbsentFromPresentAndEmpty(t *testing.T) {
	set := mustCompile(t, validation.Rules{"name": "sometimes|required|max:255"})

	if _, errs := set.Validate(url.Values{}); errs.Any() {
		t.Errorf("an absent field was rejected: %v", errs)
	}
	if _, errs := set.Validate(url.Values{"name": {""}}); !errs.Any() {
		t.Error("a field that was sent empty was accepted")
	}
	if _, errs := set.Validate(url.Values{"name": {"Ana"}}); errs.Any() {
		t.Errorf("a field that was sent was rejected: %v", errs)
	}
}

// TestBailStopsTheFieldAtItsFirstFailure, wherever it is written in the chain.
func TestBailStopsTheFieldAtItsFirstFailure(t *testing.T) {
	withBail := mustCompile(t, validation.Rules{"handle": "bail|alpha|min:8"})
	if got := errorsOf(t, withBail, url.Values{"handle": {"a-1"}})["handle"]; len(got) != 1 {
		t.Errorf("handle = %v, want one message", got)
	}

	without := mustCompile(t, validation.Rules{"handle": "alpha|min:8"})
	if got := errorsOf(t, without, url.Values{"handle": {"a-1"}})["handle"]; len(got) != 2 {
		t.Errorf("handle = %v, want both messages without bail", got)
	}
}

// TestErrorsAreOrderedTheSameOnEveryRun. Fields are evaluated in sorted order
// and rules in the order written, so a golden file does not churn.
func TestErrorsAreOrderedTheSameOnEveryRun(t *testing.T) {
	set := mustCompile(t, validation.Rules{
		"zeta":  "required",
		"alpha": "alpha|min:8",
		"mid":   "required",
	})
	form := url.Values{"alpha": {"a-1"}, "zeta": {""}, "mid": {""}}

	want := errorsOf(t, set, form).Error()
	for range 20 {
		if got := errorsOf(t, set, form).Error(); got != want {
			t.Fatalf("Error() = %q, want %q", got, want)
		}
	}
	if !strings.HasPrefix(want, "validation failed: alpha") {
		t.Errorf("Error() = %q, want the fields sorted", want)
	}
}

// TestNothingFailedMeansANilErrors, so a caller can ask Any() and a handler
// that returns it returns a map with nothing in it.
func TestNothingFailedMeansANilErrors(t *testing.T) {
	set := mustCompile(t, validation.Rules{"name": "required"})

	_, errs := set.Validate(url.Values{"name": {"Ana"}})
	if errs != nil {
		t.Errorf("Errors = %v, want nil", errs)
	}
	if errs.Any() {
		t.Error("a nil Errors reported a failure")
	}
}

// TestInputCarriesOnlyDeclaredFieldsThatPassed. A value nobody wrote a rule for
// must not be readable out of a validated request: that is how a field that was
// never checked reaches a repository.
func TestInputCarriesOnlyDeclaredFieldsThatPassed(t *testing.T) {
	set := mustCompile(t, validation.Rules{"name": "required", "age": "integer"})

	in, errs := set.Validate(url.Values{
		"name":     {"Ana"},
		"age":      {"twelve"},
		"is_admin": {"1"},
	})
	if !errs.Any() {
		t.Fatal("a non-integer age passed")
	}
	if !in.Has("name") || in.String("name") != "Ana" {
		t.Errorf("name = %q, want the value that passed", in.String("name"))
	}
	if in.Has("age") {
		t.Error("a field that failed is readable out of Input")
	}
	if in.Has("is_admin") {
		t.Error("a field the rule set does not declare is readable out of Input")
	}
}

// TestInputReadsTheTypesItsRulesProved.
func TestInputReadsTheTypesItsRulesProved(t *testing.T) {
	set := mustCompile(t, validation.Rules{
		"quantity": "required|integer",
		"price":    "required|numeric",
		"terms":    "accepted",
		"due":      "date_format:2006-01-02",
		"tags":     "sometimes",
	})
	in, errs := set.Validate(url.Values{
		"quantity": {"12"},
		"price":    {"9.99"},
		"terms":    {"on"},
		"due":      {"2026-08-10"},
		"tags":     {"go", "web"},
	})
	if errs.Any() {
		t.Fatalf("Validate: %v", errs)
	}

	if in.Int("quantity") != 12 {
		t.Errorf("Int = %d", in.Int("quantity"))
	}
	if in.Float("price") != 9.99 {
		t.Errorf("Float = %v", in.Float("price"))
	}
	// A ticked checkbox sends "on", which is what `accepted` proves and what
	// the `boolean` rule would refuse.
	if !in.Bool("terms") {
		t.Error("Bool did not read a ticked checkbox")
	}
	if got := in.Time("due", "2006-01-02"); got.Year() != 2026 || got.Month() != 8 || got.Day() != 10 {
		t.Errorf("Time = %v", got)
	}
	if got := in.Strings("tags"); !reflect.DeepEqual(got, []string{"go", "web"}) {
		t.Errorf("Strings = %v", got)
	}
}

// TestInputValuesIsACopy, so a caller cannot reach back into the request's own
// values through what validation handed it.
func TestInputValuesIsACopy(t *testing.T) {
	set := mustCompile(t, validation.Rules{"name": "required"})
	form := url.Values{"name": {"Ana"}}

	in, _ := set.Validate(form)
	out := in.Values()
	out.Set("name", "somebody else")

	if form.Get("name") != "Ana" {
		t.Errorf("the submitted form was changed through Input.Values: %v", form)
	}
	if in.String("name") != "Ana" {
		t.Errorf("Input was changed through Values(): %q", in.String("name"))
	}
}

// TestAMessageReadsWithoutAFieldNameAndWithOne. components.Field draws the bare
// sentence under a labelled input; Page.ErrorSummary puts the humanised field
// in front of it. Both have to read.
func TestAMessageReadsWithoutAFieldNameAndWithOne(t *testing.T) {
	set := mustCompile(t, validation.Rules{"password": "required|min:12"})
	errs := errorsOf(t, set, url.Values{"password": {"short"}})

	bare := errs["password"][0]
	if bare != "must be at least 12 characters" {
		t.Fatalf("message = %q", bare)
	}
	if got := validation.Humanize("password") + " " + bare; got != "Password must be at least 12 characters" {
		t.Errorf("summary = %q", got)
	}
}

// TestASizeMessageSaysCharactersOnlyWhenItCountsThem, because a bound on a
// number is not a length and saying so is wrong where somebody is reading
// carefully.
func TestASizeMessageSaysCharactersOnlyWhenItCountsThem(t *testing.T) {
	text := errorsOf(t, mustCompile(t, validation.Rules{"f": "max:3"}), url.Values{"f": {"abcd"}})
	if got := text["f"][0]; got != "must be at most 3 characters" {
		t.Errorf("message = %q", got)
	}

	number := errorsOf(t, mustCompile(t, validation.Rules{"f": "integer|max:3"}), url.Values{"f": {"4"}})
	if got := number["f"][0]; got != "must be at most 3" {
		t.Errorf("message = %q", got)
	}
}

func TestHumanizeNamesAFieldTheWayASentenceDoes(t *testing.T) {
	for _, c := range []struct{ field, want string }{
		{"password_confirmation", "Password confirmation"},
		{"email", "Email"},
		{"start-date", "Start date"},
		{"", ""},
	} {
		if got := validation.Humanize(c.field); got != c.want {
			t.Errorf("Humanize(%q) = %q, want %q", c.field, got, c.want)
		}
	}
}

// TestFieldsAndRulesForDescribeTheSet, which is what aru doctor reads.
func TestFieldsAndRulesForDescribeTheSet(t *testing.T) {
	set := mustCompile(t, validation.Rules{"title": "required|max:255", "body": "required"})

	if got := set.Fields(); !reflect.DeepEqual(got, []string{"body", "title"}) {
		t.Errorf("Fields() = %v, want them sorted", got)
	}
	if got := set.RulesFor("title"); !reflect.DeepEqual(got, []string{"required", "max"}) {
		t.Errorf("RulesFor(title) = %v, want the order written", got)
	}
	if got := set.RulesFor("nothing"); got != nil {
		t.Errorf("RulesFor of an undeclared field = %v, want nil", got)
	}
}

func errorsOf(t *testing.T, set *validation.Set, form url.Values) validation.Errors {
	t.Helper()
	_, errs := set.Validate(form)
	return errs
}
