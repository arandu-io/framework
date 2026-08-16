// Package validation checks a submitted form against a set of rules.
//
// The surface is string rules, so that a rule set reads without explanation:
//
//	var Register = validation.MustCompile(validation.Rules{
//		"name":     "required|max:255",
//		"email":    "required|email",
//		"password": "required|min:12|confirmed",
//	})
//
//	in, err := ctx.Validate(requests.Register)
//	if err != nil {
//		return err
//	}
//
// A rule set is compiled ONCE, in a package-level variable, and every rule
// string is parsed and checked there: an unknown rule, a missing or unparseable
// argument, a pattern that does not compile, a cross-field reference naming a
// field that does not exist -- each of those fails at boot, naming the field,
// the rule and the file, and all of them are reported together. A rule set that
// boots is a rule set whose names are all real.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/validation directly.
//
// The component moved to github.com/arandu-io/hesape under new names, and this
// package is now the old names pointing at it. Every symbol here answers to
// hesape/validation, with one exception: Humanize answers to
// hesape/str.Headline, because naming a field the way a sentence does is a
// string question and not a validation one.
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: where the name and the signature
// survived the move it is a Go alias, and where the design diverged it is a call
// through and nothing more.
//
// The three divergences, and what each one is:
//
//	WithMessages   is WithMessageOverrides there, because hesape spends the
//	               name WithMessages on another symbol
//	Compile        is a function VALUE rather than a wrapper, and so is
//	               MustCompile: both read runtime.Caller to name the source of
//	               a boot failure, and a wrapper would name this file
//	Humanize       reaches hesape/str.Headline, which title cases every word
//	               where this package sentence cased the first -- the one
//	               behaviour the move changes
//
// The rule table grew on the way: 59 rules shipped here and 106 ship there, and
// no rule name was dropped, so a set that compiled against this package
// compiles against hesape unchanged.
//
// The embedded time zone database that the `timezone` rule needs is
// hesape/validation's now, and arrives through the import below.
package validation
