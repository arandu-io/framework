// What this file proves is that the old name reaches the new behaviour, and
// nothing else. The rules themselves are tested in
// github.com/arandu-io/hesape/validation, against the code that now runs;
// repeating them here would be two suites over one implementation, and the one
// that drifted would be whichever nobody read.

package unit

import (
	"net/url"
	"runtime"
	"strings"
	"testing"

	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/str"
	hvalidation "github.com/arandu-io/hesape/validation"
)

// TestTheAliasesAreTheHesapeTypes is the compile-time half: each assignment
// below fails to build if the name stops being one type and becomes a copy of
// it. A copy would compile everywhere else and only show itself where a value
// crosses the boundary.
func TestTheAliasesAreTheHesapeTypes(t *testing.T) {
	var (
		_ hvalidation.Rules         = validation.Rules{}
		_ hvalidation.Messages      = validation.Messages{}
		_ hvalidation.Errors        = validation.Errors{}
		_ hvalidation.CompileError  = validation.CompileError{}
		_ hvalidation.CompileErrors = validation.CompileErrors{}
		_ hvalidation.Option        = validation.Option(nil)
		_ *hvalidation.Set          = (*validation.Set)(nil)
		_ hvalidation.Input         = validation.Input{}
		_ hvalidation.Validatable   = validation.Validatable(nil)
	)

	// And back the other way, which is what a repository written against
	// hesape and a request written against this package do to each other.
	var (
		_ validation.Rules  = hvalidation.Rules{}
		_ validation.Errors = hvalidation.Errors{}
		_ *validation.Set   = (*hvalidation.Set)(nil)
	)
}

// TestASetCompiledThroughTheOldNameValidates walks one form through the whole
// surface -- MustCompile, Validate, Errors, Input -- because that is the path
// every generated controller takes.
func TestASetCompiledThroughTheOldNameValidates(t *testing.T) {
	set := validation.MustCompile(validation.Rules{
		"email":    "required|email",
		"age":      "integer|min:18",
		"password": "required|min:12|confirmed",
	})

	in, errs := set.Validate(url.Values{
		"email":                 {"someone@example.com"},
		"age":                   {"31"},
		"password":              {"correct horse battery"},
		"password_confirmation": {"correct horse battery"},
	})
	if errs.Any() {
		t.Fatalf("a valid form failed: %v", errs)
	}
	if got := in.String("email"); got != "someone@example.com" {
		t.Errorf("Input.String(email) = %q", got)
	}
	if got := in.Int("age"); got != 31 {
		t.Errorf("Input.Int(age) = %d, want 31", got)
	}

	_, errs = set.Validate(url.Values{"email": {"not an address"}, "age": {"12"}})
	if !errs.Any() {
		t.Fatal("an invalid form passed")
	}
	if got := errs["age"]; len(got) != 1 || got[0] != "must be at least 18" {
		t.Errorf("errs[age] = %v", got)
	}
	if errs.First() == "" {
		t.Error("First() answered nothing, and it takes no argument here")
	}
}

// TestABootFailureNamesTheCallerAndNotTheBridge is why Compile and MustCompile
// are function values. hesape reads runtime.Caller to name the source that
// asked for the rule set, and a one-line wrapper here would put this bridge's
// file in every boot failure the thirteen repositories ever read.
func TestABootFailureNamesTheCallerAndNotTheBridge(t *testing.T) {
	_, wantFile, wantLine, _ := runtime.Caller(0)
	_, err := validation.Compile(validation.Rules{"email": "requried"})
	if err == nil {
		t.Fatal("a typo in a rule name compiled")
	}

	var failures validation.CompileErrors
	if !errorsAs(err, &failures) {
		t.Fatalf("err is %T, want CompileErrors", err)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %v, want one", failures)
	}
	if failures[0].File != wantFile {
		t.Errorf("File = %q, want the caller's file %q", failures[0].File, wantFile)
	}
	if failures[0].Line != wantLine+1 {
		t.Errorf("Line = %d, want %d", failures[0].Line, wantLine+1)
	}
	if strings.HasSuffix(failures[0].File, "validation/compile.go") {
		t.Errorf("File names the bridge: %q", failures[0].File)
	}

	// MustCompile reads the same frame, and it panics rather than returning.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("MustCompile did not panic on a typo")
			}
		}()
		validation.MustCompile(validation.Rules{"email": "requried"})
	}()
}

// TestWithMessagesReachesWithMessageOverrides: the only rename in the package,
// and the sentence a form shows is what proves it arrived.
func TestWithMessagesReachesWithMessageOverrides(t *testing.T) {
	set := validation.MustCompile(
		validation.Rules{"email": "required"},
		validation.WithMessages(validation.Messages{
			"email.required": "we need an address to send the receipt to",
		}),
	)

	_, errs := set.Validate(url.Values{"email": {""}})
	if got := errs["email"]; len(got) != 1 || got[0] != "we need an address to send the receipt to" {
		t.Errorf("errs[email] = %v, want the override", got)
	}
}

// TestTheOneValueHelpersReachHesape. They are six plain functions and one
// generic, all of them call-throughs, and a call-through that dropped its
// argument would still compile.
func TestTheOneValueHelpersReachHesape(t *testing.T) {
	errs := validation.Errors{}
	validation.Required(errs, "name", "  ")
	validation.MinLen(errs, "pin", "12", 4)
	validation.MaxLen(errs, "code", "abcdef", 3)
	validation.Email(errs, "email", "not an address")
	validation.NotZero(errs, "count", 0)
	validation.Confirmed(errs, "password", "one", "another")

	for _, field := range []string{"name", "pin", "code", "email", "count", "password"} {
		if len(errs[field]) != 1 {
			t.Errorf("errs[%s] = %v, want one message", field, errs[field])
		}
	}

	// And each one stays quiet on input that passes.
	ok := validation.Errors{}
	validation.Required(ok, "name", "Ada")
	validation.MinLen(ok, "pin", "1234", 4)
	validation.MaxLen(ok, "code", "abc", 3)
	validation.Email(ok, "email", "someone@example.com")
	validation.NotZero(ok, "count", 3)
	validation.Confirmed(ok, "password", "one", "one")
	if ok.Any() {
		t.Errorf("valid input produced %v", ok)
	}
}

// TestHumanizeReachesStrHeadline, including the casing it changed: this is the
// one behaviour the move alters, and it is asserted rather than left to be
// discovered from an error summary.
func TestHumanizeReachesStrHeadline(t *testing.T) {
	for _, c := range []struct{ field, want string }{
		{"password_confirmation", "Password Confirmation"},
		{"email", "Email"},
		{"", ""},
	} {
		if got := validation.Humanize(c.field); got != c.want {
			t.Errorf("Humanize(%q) = %q, want %q", c.field, got, c.want)
		}
		if got, want := validation.Humanize(c.field), str.Headline(c.field); got != want {
			t.Errorf("Humanize(%q) = %q, str.Headline = %q", c.field, got, want)
		}
	}
}

// TestValidatableIsSatisfiedByARequestWrittenAgainstEitherName. A generated
// request type declares Validate() validation.Errors against this package, and
// the handler that runs it may be written against hesape.
func TestValidatableIsSatisfiedByARequestWrittenAgainstEitherName(t *testing.T) {
	var oldName validation.Validatable = request{}
	var newName hvalidation.Validatable = request{}

	if !oldName.Validate().Any() || !newName.Validate().Any() {
		t.Error("the request reported nothing wrong")
	}
	if _, ok := oldName.(hvalidation.Validatable); !ok {
		t.Error("a Validatable of the old name is not one of the new")
	}
}

type request struct{}

func (request) Validate() validation.Errors {
	e := validation.Errors{}
	validation.Required(e, "name", "")
	return e
}

// errorsAs is errors.As, written out so that this file imports nothing it uses
// once for a type assertion the test itself is about.
func errorsAs(err error, target *validation.CompileErrors) bool {
	failures, ok := err.(validation.CompileErrors)
	if ok {
		*target = failures
	}
	return ok
}
