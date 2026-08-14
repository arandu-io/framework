// The rule set and its boot check, answered by
// github.com/arandu-io/hesape/validation.
//
// Two of the six names below diverged, and each says so where it is declared:
// WithMessages was renamed there, and Compile and MustCompile are function
// values rather than one-line wrappers, for a reason that is about
// runtime.Caller and not about style.

package validation

import hvalidation "github.com/arandu-io/hesape/validation"

// Rules is the rule set of one request, keyed by the name of the form input --
// the same name components.FieldProps.Name carries.
//
// The spelling is Laravel's, so somebody arriving from it recognises the whole
// thing without reading anything:
//
//	var Register = validation.MustCompile(validation.Rules{
//		"name":     "required|max:255",
//		"email":    "required|email",
//		"password": "required|min:12|confirmed",
//	})
//
// What a string costs is that a typo in "requried" is not a compiler error.
// What Compile buys back is that it is a BOOT error, naming the field, the rule
// and the file.
type Rules = hvalidation.Rules

// Messages overrides the sentence one rule puts on one field, keyed
// "field.rule":
//
//	validation.MustCompile(rules, validation.WithMessages(validation.Messages{
//		"email.required": "we need an address to send the receipt to",
//	}))
//
// A key naming a field or a rule the set does not declare is a boot failure: a
// typo in an override is otherwise invisible, because the default sentence is
// still there and still reads correctly.
type Messages = hvalidation.Messages

// Option adjusts a compilation.
type Option = hvalidation.Option

// WithMessages replaces the default sentence for the named field and rule.
//
// Renamed on the way to hesape: it is WithMessageOverrides there, because that
// package also carries ValidationException::withMessages and two functions
// called WithMessages in one package is one name too few.
func WithMessages(m Messages) Option { return hvalidation.WithMessageOverrides(m) }

// CompileError is one thing wrong with a rule set, named precisely enough to
// fix without opening the framework.
type CompileError = hvalidation.CompileError

// CompileErrors is everything wrong with a rule set, not the first thing.
//
// A set with three typos reports three. Reporting one at a time turns a boot
// check into three restarts, which is how a boot check gets a reputation for
// being slower than finding out on the request.
type CompileErrors = hvalidation.CompileErrors

// Compile parses and checks a rule set, and reports everything wrong with it.
//
// Use it where a rule set is built from something that is not a literal.
// Everywhere else the rule set belongs in a package-level variable, which is
// what MustCompile is for.
//
// It is a function VALUE and not the one-line wrapper every other function in
// this bridge is, and the reason is the file and line a failure names.
// hesape/validation reads runtime.Caller to find the application source that
// asked for the rule set; a wrapper is one more frame, so every boot failure
// would name this file instead of the application's -- which is precisely the
// promise CompileError.File makes. A value adds no frame. See the gap noted in
// the report: hesape has no CompileAt to hand the caller through to.
var Compile = hvalidation.Compile

// MustCompile is Compile for a package-level variable, which is where a rule
// set belongs: the check then runs before main does.
//
//	var StorePost = validation.MustCompile(validation.Rules{
//		"title": "required|max:255",
//	})
//
// It panics, for the reason view.Register panics: finding out at boot beats
// finding out from the one request that first exercises the rule.
//
// A function value, for the reason given on Compile.
var MustCompile = hvalidation.MustCompile
