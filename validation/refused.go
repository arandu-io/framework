package validation

// refused names every Laravel rule this package deliberately does not have, and
// says where the answer lives instead.
//
// It exists so that a rule set arriving from a Laravel application fails at
// boot with a sentence somebody can act on, rather than with "unknown rule" --
// which reads as an omission and invites a pull request adding it. A name in
// here is a decision, not a gap.
//
// The sentence completes: `field "x": rule "y" ` + this.
var refused = map[string]string{
	// -- The database. RULES 2 and 17: nothing reaches a repository without a
	// security.Grant, on reads as well as writes, and a rule set compiled into
	// a package-level variable holds none.
	"unique": "reaches the database, and a rule set carries no security.Grant (RULE 17). " +
		"Enforce it with a unique index and translate the conflict in the service, " +
		"which holds the Grant: return validation.Errors from Create",
	"exists": "reaches the database, and a rule set carries no security.Grant (RULE 17). " +
		"Read the row in the service, which holds the Grant, and return " +
		"validation.Errors when it is not there",
	"current_password": "needs the session and the password hasher, which a rule set has " +
		"neither of. middleware.RequireConfirmedPassword is the mechanism that exists",

	// -- PHP type juggling and array shapes. Every value in a form is a string,
	// and url.Values is flat.
	"string":   "is always true here: every value in a form is a string",
	"int":      "is Laravel's alias for integer. Write integer",
	"bool":     "is Laravel's alias for boolean. Write boolean",
	"nullable": "exists to tell null from the empty string, and a browser only ever sends the empty string. Leaving required off is the same rule",
	"array":    "needs a nested value, and a submitted form is flat. A multi-select is read with Input.Strings",
	"list":     "needs a nested value, and a submitted form is flat. A multi-select is read with Input.Strings",
	"array_keys": "needs a nested value, and a submitted form is flat. A multi-select is read " +
		"with Input.Strings",
	"required_array_keys": "needs a nested value, and a submitted form is flat. A multi-select " +
		"is read with Input.Strings",
	"in_array":       "compares against another field's list, and a submitted form is flat. Write the check in Go, in the custom block",
	"in_array_keys":  "compares against another field's list, and a submitted form is flat. Write the check in Go, in the custom block",
	"distinct":       "compares the members of one array, and a submitted form is flat",
	"contains":       "compares the members of one array, and a submitted form is flat",
	"doesnt_contain": "compares the members of one array, and a submitted form is flat",

	// -- Files. A file is not in url.Values; validating one needs a second
	// input shape, a second pipeline and sizes measured in kilobytes, which is
	// a second way to validate (RULE 9).
	"file":       "validates an upload, and an upload is not in the submitted form. Uploads arrive as their own contract",
	"image":      "validates an upload, and an upload is not in the submitted form. Uploads arrive as their own contract",
	"mimes":      "validates an upload, and an upload is not in the submitted form. Uploads arrive as their own contract",
	"mimetypes":  "validates an upload, and an upload is not in the submitted form. Uploads arrive as their own contract",
	"extensions": "validates an upload, and an upload is not in the submitted form. Uploads arrive as their own contract",
	"dimensions": "decodes an attacker-supplied image on the request path, and an upload is not in the submitted form",
	"encoding":   "validates an upload, and an upload is not in the submitted form. Uploads arrive as their own contract",

	// -- The network at request time.
	"active_url": "makes a DNS lookup inside a form submission: a round trip on the request " +
		"path with a timeout nobody set, answering a question that is not the one that " +
		"matters. An address that resolves is not an address that receives",

	// -- The conditional family beyond the four that carry the traffic.
	// Nineteen spellings of one idea; what does not fit the four is one `if` in
	// the arandu:begin custom block (RULE 15).
	"required_if_accepted":   conditional,
	"required_if_declined":   conditional,
	"required_with_all":      conditional,
	"required_without_all":   conditional,
	"prohibited_if":          conditional,
	"prohibited_if_accepted": conditional,
	"prohibited_if_declined": conditional,
	"prohibited_unless":      conditional,
	"prohibits":              conditional,
	"missing_if":             conditional,
	"missing_unless":         conditional,
	"missing_with":           conditional,
	"missing_with_all":       conditional,
	"present_if":             conditional,
	"present_unless":         conditional,
	"present_with":           conditional,
	"present_with_all":       conditional,
	"accepted_if":            conditional,
	"declined_if":            conditional,

	// -- The exclude family. They shape the array Laravel's validated()
	// returns; here the controller names each value it reads out of Input into
	// a typed struct, so a field nobody reads is already excluded.
	"exclude":         excluded,
	"exclude_if":      excluded,
	"exclude_unless":  excluded,
	"exclude_with":    excluded,
	"exclude_without": excluded,

	// -- Numeric extras and miscellany.
	"multiple_of": "is rare enough to be a line of Go in the custom block",
	"max_digits":  "is digits_between with a low bound of 1",
	"min_digits":  "is digits_between with a high bound",
	"base64":      "decodes a value nothing then reads. Decode it in the service, which is what does read it",
	"date_equals": "is after_or_equal and before_or_equal on the same moment",
	"notregex":    "is Laravel's second spelling of not_regex. Write not_regex",
}

const conditional = "is one of nineteen spellings of \"this rule applies when that field says " +
	"something\". Four carry the traffic and ship -- required_if, required_unless, " +
	"required_with, required_without. The rest is a predicate language growing by demand " +
	"(RULE 15), and what does not fit is one `if` in the arandu:begin custom block"

const excluded = "shapes the array Laravel's validated() returns. Here the controller names each " +
	"value it reads out of Input into a typed struct, so a field nobody reads is already left out"
