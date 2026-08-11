package validation_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/validation"
)

// run compiles one chain against the field "f" and returns what a form saying
// value fails on. Compilation failures are fatal: a chain a test wrote wrong is
// not a rule that behaves wrong.
func run(t *testing.T, chain, value string) validation.Errors {
	t.Helper()
	set, err := validation.Compile(validation.Rules{"f": chain})
	if err != nil {
		t.Fatalf("Compile(%q) = %v", chain, err)
	}
	_, errs := set.Validate(url.Values{"f": {value}})
	return errs
}

// TestEachRuleAcceptsAndRejectsWhatLaravelDoes is one entry per shipped rule,
// with the edge cases that are worth more than the happy path: what `required`
// does with "0", what `min` counts, what `boolean` refuses.
func TestEachRuleAcceptsAndRejectsWhatLaravelDoes(t *testing.T) {
	cases := []struct {
		chain string
		good  []string
		bad   []string
	}{
		// "0" is an answer; three spaces are not. Laravel trims before asking.
		{"required", []string{"0", "false", "a"}, []string{"", " ", "\t\n"}},
		{"filled", []string{"0", "a"}, []string{" "}},
		{"prohibited", []string{"", "   "}, []string{"a", "0"}},

		// Consent takes Laravel's lists exactly. "on" is what a ticked checkbox
		// sends; "yes" and "true" are what an API client sends instead.
		{"accepted", []string{"yes", "on", "1", "true"}, []string{"", "0", "no", "TRUE", "ok"}},
		{"declined", []string{"no", "off", "0", "false"}, []string{"", "1", "yes", "FALSE"}},

		// Size on a string counts RUNES. "José" is four characters and five
		// bytes, and a limit in bytes rejects it.
		{"min:4", []string{"José", "abcd", "abcde", ""}, []string{"abc"}},
		{"max:4", []string{"José", "", "abcd"}, []string{"abcde"}},
		{"size:4", []string{"José", "abcd"}, []string{"abc", "abcde"}},
		{"between:2,4", []string{"ab", "José"}, []string{"a", "abcde"}},

		// The same words on a numeric field mean the VALUE, not the length.
		{"integer|min:12", []string{"12", "1200"}, []string{"11", "-3"}},
		{"integer|max:12", []string{"12", "-3"}, []string{"13", "1200"}},
		{"numeric|between:1.5,2.5", []string{"1.5", "2", "2.50"}, []string{"1.49", "3"}},

		{"digits:4", []string{"0000", "1234"}, []string{"123", "12345", "12a4", "+123"}},
		{"digits_between:2,4", []string{"12", "1234"}, []string{"1", "12345", "12.3"}},

		{"numeric", []string{"0", "-1", "1.5", ".5", "1e3", "+2"}, []string{"a", "1,5", "0x1A", "Inf", "NaN", "1 "}},
		// "012" is refused, as PHP's FILTER_VALIDATE_INT refuses it. "" is not
		// in either list: a rule that is not implicit does not run on a blank
		// value, which TestANonImplicitRuleDoesNotRunOnABlankValue is about.
		{"integer", []string{"0", "-1", "12", "+3"}, []string{"1.0", "012", "a"}},
		{"decimal:2", []string{"1.00", "0.15", "-3.99"}, []string{"1.0", "1", "1.000", "1e2"}},
		{"decimal:1,3", []string{"1.0", "1.00", "1.000"}, []string{"1", "1.0000"}},

		// Laravel's boolean accepts only "0" and "1" from a form. A ticked
		// checkbox sends "on", which is what `accepted` is for.
		{"boolean", []string{"0", "1"}, []string{"true", "on", "yes", "2"}},
		{"ascii", []string{"abc", "a-1_2"}, []string{"José", "ação"}},
		{"json", []string{`{"a":1}`, `[]`, `1`, `"x"`}, []string{`{`, `{a:1}`}},

		{"date", []string{"2026-08-10", "2026-08-10 09:30:00", "2026-08-10T09:30:00Z"}, []string{"10/08/2026", "next thursday", "2026-13-01"}},
		// A Go layout, never a PHP one. The round trip is what refuses
		// "2026-8-1" for the layout "2006-01-02".
		{"date_format:2006-01-02", []string{"2026-08-10"}, []string{"2026-8-1", "2026-08-10T00:00:00Z", "10/08/2026"}},
		{"date_format:02/01/2006", []string{"10/08/2026"}, []string{"2026-08-10"}},

		{"email", []string{"a@b.co", "someone.else@sub.example.com"}, []string{"a", "@b.co", "a@", "a@b", "a b@c.co"}},
		{"url", []string{"https://example.com", "http://example.com/a?b=1"}, []string{"example.com", "ftp://example.com", "javascript:alert(1)", "https://"}},
		{"uuid", []string{"0f3a5f5e-4d2b-4e3a-9c1d-1f2e3a4b5c6d"}, []string{"0f3a5f5e4d2b4e3a9c1d1f2e3a4b5c6d", "not-a-uuid"}},
		{"ulid", []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV"}, []string{"01ARZ3NDEKTSV4RRFFQ69G5FA", "81ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAU"}},

		// alpha defaults to Unicode, as Laravel's does, and takes :ascii.
		{"alpha", []string{"abc", "José", "ação"}, []string{"ab1", "a-b", "a b"}},
		{"alpha:ascii", []string{"abc"}, []string{"José"}},
		{"alpha_dash", []string{"a-b_1", "José-1"}, []string{"a b", "a.b"}},
		{"alpha_dash:ascii", []string{"a-b_1"}, []string{"José-1"}},
		{"alpha_num", []string{"ab1", "José1"}, []string{"a-b", "a b"}},
		{"alpha_num:ascii", []string{"ab1"}, []string{"José1"}},
		{"lowercase", []string{"abc", "a-1", "ação"}, []string{"Abc", "ABC"}},
		{"uppercase", []string{"ABC", "A-1"}, []string{"Abc", "abc"}},

		{"starts_with:a,b", []string{"abc", "bcd"}, []string{"cde"}},
		{"ends_with:.com,.org", []string{"x.com", "x.org"}, []string{"x.net"}},
		{"doesnt_start_with:a,b", []string{"cde"}, []string{"abc", "bcd"}},
		{"doesnt_end_with:.com", []string{"x.org"}, []string{"x.com"}},

		{"in:red,green", []string{"red", "green"}, []string{"blue", "Red"}},
		{"not_in:red,green", []string{"blue"}, []string{"red"}},

		{"ip", []string{"192.168.0.1", "::1"}, []string{"192.168.0.256", "01.2.3.4", "fe80::1%eth0"}},
		{"ipv4", []string{"192.168.0.1"}, []string{"::1", "::ffff:192.168.0.1"}},
		{"ipv6", []string{"::1", "::ffff:192.168.0.1"}, []string{"192.168.0.1"}},
		{"mac_address", []string{"00:11:22:33:44:55", "00-11-22-33-44-55", "0011.2233.4455"}, []string{"00:11:22:33:44", "zz:11:22:33:44:55"}},
		{"hex_color", []string{"#fff", "#ffffff", "#ffff", "#ffffffff"}, []string{"fff", "#ff", "#gggggg"}},
		{"timezone", []string{"UTC", "America/Sao_Paulo"}, []string{"Local", "Mars/Olympus", "BRT"}},

		{"regex:^[a-z]+$", []string{"abc"}, []string{"abc1", "ABC"}},
		{"not_regex:^[a-z]+$", []string{"abc1"}, []string{"abc"}},
	}

	for _, c := range cases {
		t.Run(c.chain, func(t *testing.T) {
			for _, v := range c.good {
				if errs := run(t, c.chain, v); errs.Any() {
					t.Errorf("%q was rejected: %v", v, errs)
				}
			}
			for _, v := range c.bad {
				if errs := run(t, c.chain, v); !errs.Any() {
					t.Errorf("%q was accepted", v)
				}
			}
		})
	}
}

// TestSizeCountsRunesForAStringAndValueForANumber is Laravel's $numericRules
// gate, and it is silent when it is wrong: "5" is one character and the number
// five, so max:3 says opposite things about the same box.
func TestSizeCountsRunesForAStringAndValueForANumber(t *testing.T) {
	if errs := run(t, "max:3", "12345"); !errs.Any() {
		t.Error("five characters passed max:3 on a string field")
	}
	if errs := run(t, "integer|max:3", "2"); errs.Any() {
		t.Errorf("the number 2 failed max:3 on an integer field: %v", errs)
	}
	// Both halves of the gate: a field that declares integer but was sent a
	// word has no number to measure, so it is measured as characters.
	if errs := run(t, "integer|max:3", "abcd"); !errs.Any() {
		t.Error("a four-character word passed max:3 on an integer field")
	}
}

// TestConfirmedComparesTheFieldLaravelNames: the default other box is
// <field>_confirmation, and an explicit argument replaces it.
func TestConfirmedComparesTheFieldLaravelNames(t *testing.T) {
	set := mustCompile(t, validation.Rules{"password": "required|confirmed"})

	_, errs := set.Validate(url.Values{"password": {"correct horse"}, "password_confirmation": {"correct horse"}})
	if errs.Any() {
		t.Errorf("a matching confirmation was rejected: %v", errs)
	}

	_, errs = set.Validate(url.Values{"password": {"correct horse"}, "password_confirmation": {"typo"}})
	if got := errs["password"]; len(got) != 1 || got[0] != "does not match" {
		t.Errorf("password = %v, want one \"does not match\"", got)
	}

	// The message goes on the field, not on the confirmation: a form that
	// reports it next to the second box gets the second box retyped.
	if len(errs["password_confirmation"]) != 0 {
		t.Errorf("the confirmation was blamed as well: %v", errs)
	}
}

// TestSameAndDifferentReadTheOtherFieldAsLaravelDoes: `same` compares against
// the empty string when the other box was not sent, and `different` does not
// compare at all -- Laravel's asymmetry, and copying it is the point.
func TestSameAndDifferentReadTheOtherFieldAsLaravelDoes(t *testing.T) {
	set := mustCompile(t, validation.Rules{"a": "same:b", "b": "sometimes"})
	if _, errs := set.Validate(url.Values{"a": {"x"}}); !errs.Any() {
		t.Error("same passed against a field that was not sent")
	}

	set = mustCompile(t, validation.Rules{"a": "different:b", "b": "sometimes"})
	if _, errs := set.Validate(url.Values{"a": {"x"}}); errs.Any() {
		t.Errorf("different failed against a field that was not sent: %v", errs)
	}
	if _, errs := set.Validate(url.Values{"a": {"x"}, "b": {"x"}}); !errs.Any() {
		t.Error("different passed two equal values")
	}
}

// TestRequiredIfOnlyFiresWhenTheOtherFieldWasSent: Laravel returns early when
// the other key is absent rather than treating its absence as a value, so a
// form that does not carry the field cannot be answering it.
func TestRequiredIfOnlyFiresWhenTheOtherFieldWasSent(t *testing.T) {
	set := mustCompile(t, validation.Rules{"reason": "required_if:status,rejected", "status": "sometimes"})

	if _, errs := set.Validate(url.Values{}); errs.Any() {
		t.Errorf("required_if fired with no status at all: %v", errs)
	}
	if _, errs := set.Validate(url.Values{"status": {"approved"}}); errs.Any() {
		t.Errorf("required_if fired on another status: %v", errs)
	}
	_, errs := set.Validate(url.Values{"status": {"rejected"}})
	if got := errs["reason"]; len(got) != 1 || !strings.Contains(got[0], "when Status is rejected") {
		t.Errorf("reason = %v, want the message to name the other field", got)
	}
}

// TestRequiredUnlessFiresWhenTheOtherFieldIsAbsent is the twin asymmetry:
// unlike required_if, it has no early return, so an absent field means the
// condition is not met and the value is demanded.
func TestRequiredUnlessFiresWhenTheOtherFieldIsAbsent(t *testing.T) {
	set := mustCompile(t, validation.Rules{"reason": "required_unless:status,approved", "status": "sometimes"})

	if _, errs := set.Validate(url.Values{}); !errs.Any() {
		t.Error("required_unless passed with no status at all")
	}
	if _, errs := set.Validate(url.Values{"status": {"approved"}}); errs.Any() {
		t.Errorf("required_unless fired on the excusing value: %v", errs)
	}
}

// TestRequiredWithAndWithoutAskAboutAnyOfTheirFields, which is Laravel's
// allFailingRequired/anyFailingRequired pair read the right way round.
func TestRequiredWithAndWithoutAskAboutAnyOfTheirFields(t *testing.T) {
	with := mustCompile(t, validation.Rules{"city": "required_with:street,postcode"})
	if _, errs := with.Validate(url.Values{}); errs.Any() {
		t.Errorf("required_with fired with neither field filled: %v", errs)
	}
	if _, errs := with.Validate(url.Values{"street": {"Rua A"}}); !errs.Any() {
		t.Error("required_with passed with one field filled")
	}

	without := mustCompile(t, validation.Rules{"email": "required_without:phone"})
	if _, errs := without.Validate(url.Values{"phone": {"+55 11"}}); errs.Any() {
		t.Errorf("required_without fired with the other field filled: %v", errs)
	}
	if _, errs := without.Validate(url.Values{"phone": {"  "}}); !errs.Any() {
		t.Error("required_without passed with the other field blank")
	}
}

// TestComparisonsReadAnotherFieldAndMeasureLikeSizeDoes: gt/gte/lt/lte take a
// field name only, and what they compare is the same size that min and max use.
func TestComparisonsReadAnotherFieldAndMeasureLikeSizeDoes(t *testing.T) {
	numbers := mustCompile(t, validation.Rules{"high": "integer|gt:low", "low": "integer"})
	if _, errs := numbers.Validate(url.Values{"high": {"9"}, "low": {"10"}}); !errs.Any() {
		t.Error("9 passed gt:low against 10")
	}
	if _, errs := numbers.Validate(url.Values{"high": {"11"}, "low": {"10"}}); errs.Any() {
		t.Errorf("11 failed gt:low against 10: %v", errs)
	}

	// Without a numeric rule the comparison is by length, exactly as Laravel's
	// getSize is -- "9" is not greater than "10", it is shorter.
	words := mustCompile(t, validation.Rules{"long": "gt:short", "short": "sometimes"})
	if _, errs := words.Validate(url.Values{"long": {"9"}, "short": {"10"}}); !errs.Any() {
		t.Error("a one-character value passed gt against a two-character one")
	}
}

// TestDatesCompareAgainstAFieldAKeywordOrALiteral, which are the three things
// after: and before: accept and the only three.
func TestDatesCompareAgainstAFieldAKeywordOrALiteral(t *testing.T) {
	set := mustCompile(t, validation.Rules{
		"starts": "date",
		"ends":   "date|after:starts",
	})
	if _, errs := set.Validate(url.Values{"starts": {"2026-01-01"}, "ends": {"2025-12-31"}}); !errs.Any() {
		t.Error("an end before the start passed after:starts")
	}
	if _, errs := set.Validate(url.Values{"starts": {"2026-01-01"}, "ends": {"2026-01-02"}}); errs.Any() {
		t.Errorf("an end after the start failed after:starts: %v", errs)
	}

	keyword := mustCompile(t, validation.Rules{"when": "date|after:today"})
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if _, errs := keyword.Validate(url.Values{"when": {tomorrow}}); errs.Any() {
		t.Errorf("tomorrow failed after:today: %v", errs)
	}
	if _, errs := keyword.Validate(url.Values{"when": {yesterday}}); !errs.Any() {
		t.Error("yesterday passed after:today")
	}

	literal := mustCompile(t, validation.Rules{"when": "date|before_or_equal:2026-01-01"})
	if _, errs := literal.Validate(url.Values{"when": {"2026-01-01"}}); errs.Any() {
		t.Errorf("the boundary failed before_or_equal: %v", errs)
	}
	if _, errs := literal.Validate(url.Values{"when": {"2026-01-02"}}); !errs.Any() {
		t.Error("a later date passed before_or_equal")
	}
}

// TestADateComparisonUsesTheFieldsOwnLayout, because a field that declares
// date_format writes its dates that way and its bound is written the same way.
func TestADateComparisonUsesTheFieldsOwnLayout(t *testing.T) {
	set := mustCompile(t, validation.Rules{"when": "date_format:02/01/2006|after:01/01/2026"})
	if _, errs := set.Validate(url.Values{"when": {"02/01/2026"}}); errs.Any() {
		t.Errorf("a later date in the declared layout failed: %v", errs)
	}
	if _, errs := set.Validate(url.Values{"when": {"31/12/2025"}}); !errs.Any() {
		t.Error("an earlier date in the declared layout passed")
	}
}

// TestTimezoneAnswersOnAMachineWithNoZoneDatabase is why time/tzdata is
// imported: without it this rule rejects every zone on a scratch container,
// which is the one deployment nobody tests.
func TestTimezoneAnswersOnAMachineWithNoZoneDatabase(t *testing.T) {
	t.Setenv("ZONEINFO", "/nonexistent-on-purpose")
	if errs := run(t, "timezone", "America/Sao_Paulo"); errs.Any() {
		t.Errorf("a real zone was rejected: %v -- is time/tzdata still imported?", errs)
	}
}

func mustCompile(t *testing.T, rules validation.Rules) *validation.Set {
	t.Helper()
	set, err := validation.Compile(rules)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return set
}
