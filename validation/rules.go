package validation

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// spec is one rule name: how many arguments it takes, whether it runs on a
// blank value, what it checks at boot, what it answers on a request and what it
// says when it fails.
//
// Everything about a rule lives in its entry. Adding one is a single edit, and
// reading one does not mean opening four files to find the message, the arity
// and the boot check separately -- which is how a rule ends up accepting an
// argument nobody validates.
type spec struct {
	// minArgs and maxArgs bound the argument list. A maxArgs of -1 is
	// unbounded; both zero means the rule takes no argument, and giving it one
	// is a boot failure rather than a silently ignored string.
	minArgs, maxArgs int

	// implicit marks a rule that runs even when the value is blank, and whose
	// failure stops every later rule on the field.
	//
	// Without it, `min:12` on an optional field nobody filled in reports "must
	// be at least 12 characters" about a box the person deliberately left
	// alone. It is Laravel's $implicitRules, and dropping it is a real bug.
	implicit bool

	// sizeIsValue marks numeric, integer and decimal: the three rules that make
	// min, max, size, between and the four comparisons measure the VALUE rather
	// than the number of characters. Laravel's $numericRules, and a developer
	// arriving from it relies on the difference.
	sizeIsValue bool

	// refs are the argument positions that name another field of the same set.
	// Each is checked at boot: a rule pointing at a field name that does not
	// exist never fires, and nothing says so.
	refs []int

	// check runs at boot, after every field's flags are known. It rejects a
	// malformed argument and parses whatever the request should not parse
	// again.
	check func(c *checkCtx) error

	// eval answers whether the value passes. It never allocates a message.
	eval func(e *eval, r *rule) bool

	// message is the sentence a failure puts on the field. It is written to
	// read after a humanised field name and to be drawn without one -- see
	// Page.ErrorSummary and components.Field.
	message func(f *field, r *rule) string
}

// specs is the whole rule set. It is closed: a name that is not in here and not
// in refused is a boot failure, which is what makes "a rule set that boots is a
// rule set whose names are all real" true.
//
// 61 of Laravel's 111, at Laravel's spelling. Every one of the other 50 is
// named in refused with the reason it is not here, so a migrating developer
// gets an answer rather than "unknown rule".
var specs = map[string]*spec{
	// ---------------------------------------------------------------------
	// Presence and flow.
	// ---------------------------------------------------------------------
	"required": {
		implicit: true,
		eval:     func(e *eval, r *rule) bool { return filled(e.value) },
		message:  func(f *field, r *rule) string { return "is required" },
	},
	"sometimes": {
		// A marker. The field is skipped entirely when its key is absent, which
		// is decided in Set.Validate, so this can only pass.
		eval:    func(e *eval, r *rule) bool { return true },
		message: func(f *field, r *rule) string { return "" },
	},
	"bail": {
		// A marker. Set.Validate reads field.bail; see shouldStop.
		eval:    func(e *eval, r *rule) bool { return true },
		message: func(f *field, r *rule) string { return "" },
	},
	"filled": {
		implicit: true,
		// Absent passes: filled is about a key that was sent, not about one
		// that had to be. That is the whole difference from required.
		eval: func(e *eval, r *rule) bool {
			if !e.has(e.f.name) {
				return true
			}
			return filled(e.value)
		},
		message: func(f *field, r *rule) string { return "must have a value" },
	},
	"present": {
		implicit: true,
		eval:     func(e *eval, r *rule) bool { return e.has(e.f.name) },
		message:  func(f *field, r *rule) string { return "must be present" },
	},
	"missing": {
		implicit: true,
		eval:     func(e *eval, r *rule) bool { return !e.has(e.f.name) },
		message:  func(f *field, r *rule) string { return "must not be sent" },
	},
	"prohibited": {
		// Implicit here although Laravel leaves it out of $implicitRules. The
		// consequence is that a prohibited field somebody filled in gets one
		// message instead of also being told it is too long -- and a blank
		// value passes either way, so nothing else changes.
		implicit: true,
		eval:     func(e *eval, r *rule) bool { return !filled(e.value) },
		message:  func(f *field, r *rule) string { return "is not allowed" },
	},
	"required_if": {
		minArgs: 2, maxArgs: -1, implicit: true, refs: []int{0},
		eval: func(e *eval, r *rule) bool {
			// Laravel returns early when the other key was not sent at all,
			// rather than treating its absence as the empty string: a form that
			// does not carry the field cannot be answering it.
			if !e.has(r.args[0]) {
				return true
			}
			if !contains(r.args[1:], e.other(r.args[0])) {
				return true
			}
			return filled(e.value)
		},
		message: func(f *field, r *rule) string {
			return fmt.Sprintf("is required when %s is %s", Humanize(r.args[0]), or(r.args[1:]))
		},
	},
	"required_unless": {
		minArgs: 2, maxArgs: -1, implicit: true, refs: []int{0},
		eval: func(e *eval, r *rule) bool {
			if contains(r.args[1:], e.other(r.args[0])) {
				return true
			}
			return filled(e.value)
		},
		message: func(f *field, r *rule) string {
			return fmt.Sprintf("is required unless %s is %s", Humanize(r.args[0]), or(r.args[1:]))
		},
	},
	"required_with": {
		minArgs: 1, maxArgs: -1, implicit: true,
		eval: func(e *eval, r *rule) bool {
			for _, other := range r.args {
				if filled(e.other(other)) {
					return filled(e.value)
				}
			}
			return true
		},
		message: func(f *field, r *rule) string {
			return fmt.Sprintf("is required when %s is present", andHumanized(r.args))
		},
	},
	"required_without": {
		minArgs: 1, maxArgs: -1, implicit: true,
		eval: func(e *eval, r *rule) bool {
			for _, other := range r.args {
				if !filled(e.other(other)) {
					return filled(e.value)
				}
			}
			return true
		},
		message: func(f *field, r *rule) string {
			return fmt.Sprintf("is required when %s is not present", andHumanized(r.args))
		},
	},

	// ---------------------------------------------------------------------
	// Cross-field and consent.
	// ---------------------------------------------------------------------
	"confirmed": {
		maxArgs: 1, refs: []int{0},
		eval: func(e *eval, r *rule) bool { return e.value == e.other(confirmationOf(e.f.name, r)) },
		// The message goes on the field rather than on the confirmation, and it
		// says what is wrong rather than which box to change: a form that
		// reports "does not match" next to the first box gets the first box
		// edited, and fails again.
		message: func(f *field, r *rule) string { return "does not match" },
	},
	"same": {
		minArgs: 1, maxArgs: 1, refs: []int{0},
		eval:    func(e *eval, r *rule) bool { return e.value == e.other(r.args[0]) },
		message: func(f *field, r *rule) string { return "must match " + Humanize(r.args[0]) },
	},
	"different": {
		minArgs: 1, maxArgs: 1, refs: []int{0},
		eval: func(e *eval, r *rule) bool {
			// Only compared when the other key was sent, as Laravel does: a
			// field that is not in the form is not the same as this one.
			if !e.has(r.args[0]) {
				return true
			}
			return e.value != e.other(r.args[0])
		},
		message: func(f *field, r *rule) string { return "must be different from " + Humanize(r.args[0]) },
	},
	"accepted": {
		implicit: true,
		eval:     func(e *eval, r *rule) bool { return filled(e.value) && contains(acceptable, e.value) },
		message:  func(f *field, r *rule) string { return "must be accepted" },
	},
	"declined": {
		implicit: true,
		eval:     func(e *eval, r *rule) bool { return filled(e.value) && contains(declinable, e.value) },
		message:  func(f *field, r *rule) string { return "must be declined" },
	},

	// ---------------------------------------------------------------------
	// Size. Polymorphic exactly as Laravel's is: see eval.size.
	// ---------------------------------------------------------------------
	"min": {
		minArgs: 1, maxArgs: 1, check: needSizes,
		eval:    func(e *eval, r *rule) bool { return e.size(e.value) >= r.nums[0] },
		message: func(f *field, r *rule) string { return "must be at least " + measure(f, r.args[0]) },
	},
	"max": {
		minArgs: 1, maxArgs: 1, check: needSizes,
		eval:    func(e *eval, r *rule) bool { return e.size(e.value) <= r.nums[0] },
		message: func(f *field, r *rule) string { return "must be at most " + measure(f, r.args[0]) },
	},
	"size": {
		minArgs: 1, maxArgs: 1, check: needSizes,
		eval:    func(e *eval, r *rule) bool { return e.size(e.value) == r.nums[0] },
		message: func(f *field, r *rule) string { return "must be exactly " + measure(f, r.args[0]) },
	},
	"between": {
		minArgs: 2, maxArgs: 2, check: chain(needSizes, ascending),
		eval: func(e *eval, r *rule) bool {
			n := e.size(e.value)
			return n >= r.nums[0] && n <= r.nums[1]
		},
		message: func(f *field, r *rule) string {
			return fmt.Sprintf("must be between %s and %s", r.args[0], measure(f, r.args[1]))
		},
	},
	"digits": {
		minArgs: 1, maxArgs: 1, check: needWholeNumbers,
		eval: func(e *eval, r *rule) bool {
			return digitsOnly(e.value) && float64(len(e.value)) == r.nums[0]
		},
		message: func(f *field, r *rule) string { return "must be " + r.args[0] + " digits" },
	},
	"digits_between": {
		minArgs: 2, maxArgs: 2, check: chain(needWholeNumbers, ascending),
		eval: func(e *eval, r *rule) bool {
			n := float64(len(e.value))
			return digitsOnly(e.value) && n >= r.nums[0] && n <= r.nums[1]
		},
		message: func(f *field, r *rule) string {
			return fmt.Sprintf("must be between %s and %s digits", r.args[0], r.args[1])
		},
	},
	"gt": {
		minArgs: 1, maxArgs: 1, refs: []int{0},
		eval:    func(e *eval, r *rule) bool { return e.size(e.value) > e.size(e.other(r.args[0])) },
		message: func(f *field, r *rule) string { return "must be greater than " + Humanize(r.args[0]) },
	},
	"gte": {
		minArgs: 1, maxArgs: 1, refs: []int{0},
		eval: func(e *eval, r *rule) bool { return e.size(e.value) >= e.size(e.other(r.args[0])) },
		message: func(f *field, r *rule) string {
			return "must be greater than or equal to " + Humanize(r.args[0])
		},
	},
	"lt": {
		minArgs: 1, maxArgs: 1, refs: []int{0},
		eval:    func(e *eval, r *rule) bool { return e.size(e.value) < e.size(e.other(r.args[0])) },
		message: func(f *field, r *rule) string { return "must be less than " + Humanize(r.args[0]) },
	},
	"lte": {
		minArgs: 1, maxArgs: 1, refs: []int{0},
		eval: func(e *eval, r *rule) bool { return e.size(e.value) <= e.size(e.other(r.args[0])) },
		message: func(f *field, r *rule) string {
			return "must be less than or equal to " + Humanize(r.args[0])
		},
	},

	// ---------------------------------------------------------------------
	// Type.
	// ---------------------------------------------------------------------
	"numeric": {
		sizeIsValue: true,
		eval:        func(e *eval, r *rule) bool { _, ok := number(e.value); return ok },
		message:     func(f *field, r *rule) string { return "must be a number" },
	},
	"integer": {
		sizeIsValue: true,
		eval:        func(e *eval, r *rule) bool { _, ok := whole(e.value); return ok },
		message:     func(f *field, r *rule) string { return "must be a whole number" },
	},
	"decimal": {
		minArgs: 1, maxArgs: 2, sizeIsValue: true, check: chain(needWholeNumbers, ascending),
		eval: func(e *eval, r *rule) bool {
			n, ok := decimalPlaces(e.value)
			if !ok {
				return false
			}
			if len(r.nums) == 1 {
				return float64(n) == r.nums[0]
			}
			return float64(n) >= r.nums[0] && float64(n) <= r.nums[1]
		},
		message: func(f *field, r *rule) string {
			if len(r.args) == 1 {
				return "must have " + r.args[0] + " decimal places"
			}
			return fmt.Sprintf("must have between %s and %s decimal places", r.args[0], r.args[1])
		},
	},
	"boolean": {
		// Laravel accepts only "0" and "1" from a form, and so does this. A
		// checkbox sends "on", which is what `accepted` is for -- copying
		// Laravel's list exactly matters more here than being accommodating,
		// because somebody migrating will not re-test a rule that looks the
		// same.
		eval:    func(e *eval, r *rule) bool { return e.value == "0" || e.value == "1" },
		message: func(f *field, r *rule) string { return "must be true or false" },
	},
	"ascii": {
		eval: func(e *eval, r *rule) bool {
			for i := range len(e.value) {
				if e.value[i] > unicode.MaxASCII {
					return false
				}
			}
			return true
		},
		message: func(f *field, r *rule) string { return "must contain only ASCII characters" },
	},
	"json": {
		eval:    func(e *eval, r *rule) bool { return json.Valid([]byte(e.value)) },
		message: func(f *field, r *rule) string { return "must be valid JSON" },
	},

	// ---------------------------------------------------------------------
	// Dates. The layout is a Go layout; see checkLayout.
	// ---------------------------------------------------------------------
	"date": {
		eval:    func(e *eval, r *rule) bool { _, ok := parseDate("", e.value); return ok },
		message: func(f *field, r *rule) string { return "is not a valid date" },
	},
	"date_format": {
		minArgs: 1, maxArgs: 1, check: checkLayout,
		eval: func(e *eval, r *rule) bool {
			t, err := time.Parse(r.args[0], e.value)
			// The round trip is Laravel's `$date->format($format) == $value`:
			// time.Parse accepts "2006-1-2" for the layout "2006-01-02", and a
			// format that accepts two spellings of one day is not a format.
			return err == nil && t.Format(r.args[0]) == e.value
		},
		message: func(f *field, r *rule) string { return "must match the format " + r.args[0] },
	},
	"after": {
		minArgs: 1, maxArgs: 1, check: checkMoment,
		eval:    func(e *eval, r *rule) bool { return e.compareDates(r, func(c int) bool { return c > 0 }) },
		message: func(f *field, r *rule) string { return "must be a date after " + moment(r.args[0]) },
	},
	"after_or_equal": {
		minArgs: 1, maxArgs: 1, check: checkMoment,
		eval:    func(e *eval, r *rule) bool { return e.compareDates(r, func(c int) bool { return c >= 0 }) },
		message: func(f *field, r *rule) string { return "must be a date on or after " + moment(r.args[0]) },
	},
	"before": {
		minArgs: 1, maxArgs: 1, check: checkMoment,
		eval:    func(e *eval, r *rule) bool { return e.compareDates(r, func(c int) bool { return c < 0 }) },
		message: func(f *field, r *rule) string { return "must be a date before " + moment(r.args[0]) },
	},
	"before_or_equal": {
		minArgs: 1, maxArgs: 1, check: checkMoment,
		eval:    func(e *eval, r *rule) bool { return e.compareDates(r, func(c int) bool { return c <= 0 }) },
		message: func(f *field, r *rule) string { return "must be a date on or before " + moment(r.args[0]) },
	},

	// ---------------------------------------------------------------------
	// String shape.
	// ---------------------------------------------------------------------
	"email": {
		// Shape only. Deliverability is proven by sending mail, and a DNS
		// lookup inside a form submission is a network round trip with a
		// timeout nobody set, answering a question that is not the one asked.
		//
		// It accepts an argument list only so that Laravel's `email:dns` gets
		// the reason rather than "takes no argument", which reads as an
		// oversight.
		maxArgs: -1, check: refuseEmailArguments,
		eval:    func(e *eval, r *rule) bool { return emailShape(e.value) },
		message: func(f *field, r *rule) string { return "is not a valid email address" },
	},
	"url": {
		eval:    func(e *eval, r *rule) bool { return isURL(e.value) },
		message: func(f *field, r *rule) string { return "is not a valid URL" },
	},
	"uuid": {
		eval:    func(e *eval, r *rule) bool { return uuidShape.MatchString(e.value) },
		message: func(f *field, r *rule) string { return "is not a valid UUID" },
	},
	"ulid": {
		eval:    func(e *eval, r *rule) bool { return ulidShape.MatchString(e.value) },
		message: func(f *field, r *rule) string { return "is not a valid ULID" },
	},
	"alpha": {
		maxArgs: 1, check: checkAscii,
		eval:    func(e *eval, r *rule) bool { return alphabet(r, alphaUnicode, alphaASCII).MatchString(e.value) },
		message: func(f *field, r *rule) string { return "must contain only letters" },
	},
	"alpha_dash": {
		maxArgs: 1, check: checkAscii,
		eval: func(e *eval, r *rule) bool { return alphabet(r, dashUnicode, dashASCII).MatchString(e.value) },
		message: func(f *field, r *rule) string {
			return "must contain only letters, numbers, dashes and underscores"
		},
	},
	"alpha_num": {
		maxArgs: 1, check: checkAscii,
		eval:    func(e *eval, r *rule) bool { return alphabet(r, numUnicode, numASCII).MatchString(e.value) },
		message: func(f *field, r *rule) string { return "must contain only letters and numbers" },
	},
	"lowercase": {
		eval:    func(e *eval, r *rule) bool { return strings.ToLower(e.value) == e.value },
		message: func(f *field, r *rule) string { return "must be lowercase" },
	},
	"uppercase": {
		eval:    func(e *eval, r *rule) bool { return strings.ToUpper(e.value) == e.value },
		message: func(f *field, r *rule) string { return "must be uppercase" },
	},
	"starts_with": {
		minArgs: 1, maxArgs: -1,
		eval:    func(e *eval, r *rule) bool { return anyAffix(r.args, strings.HasPrefix, e.value) },
		message: func(f *field, r *rule) string { return "must start with " + or(r.args) },
	},
	"ends_with": {
		minArgs: 1, maxArgs: -1,
		eval:    func(e *eval, r *rule) bool { return anyAffix(r.args, strings.HasSuffix, e.value) },
		message: func(f *field, r *rule) string { return "must end with " + or(r.args) },
	},
	"doesnt_start_with": {
		minArgs: 1, maxArgs: -1,
		eval:    func(e *eval, r *rule) bool { return !anyAffix(r.args, strings.HasPrefix, e.value) },
		message: func(f *field, r *rule) string { return "must not start with " + or(r.args) },
	},
	"doesnt_end_with": {
		minArgs: 1, maxArgs: -1,
		eval:    func(e *eval, r *rule) bool { return !anyAffix(r.args, strings.HasSuffix, e.value) },
		message: func(f *field, r *rule) string { return "must not end with " + or(r.args) },
	},

	// ---------------------------------------------------------------------
	// Membership and pattern.
	// ---------------------------------------------------------------------
	"in": {
		minArgs: 1, maxArgs: -1,
		eval:    func(e *eval, r *rule) bool { return contains(r.args, e.value) },
		message: func(f *field, r *rule) string { return "is not one of the allowed values" },
	},
	"not_in": {
		minArgs: 1, maxArgs: -1,
		eval:    func(e *eval, r *rule) bool { return !contains(r.args, e.value) },
		message: func(f *field, r *rule) string { return "is not one of the allowed values" },
	},
	"regex": {
		minArgs: 1, maxArgs: 1, check: compilePattern,
		eval:    func(e *eval, r *rule) bool { return r.re.MatchString(e.value) },
		message: func(f *field, r *rule) string { return "is not in the expected format" },
	},
	"not_regex": {
		minArgs: 1, maxArgs: 1, check: compilePattern,
		eval:    func(e *eval, r *rule) bool { return !r.re.MatchString(e.value) },
		message: func(f *field, r *rule) string { return "is not in the expected format" },
	},

	// ---------------------------------------------------------------------
	// Network and colour shape.
	// ---------------------------------------------------------------------
	"ip": {
		eval:    func(e *eval, r *rule) bool { _, ok := address(e.value); return ok },
		message: func(f *field, r *rule) string { return "is not a valid IP address" },
	},
	"ipv4": {
		eval: func(e *eval, r *rule) bool {
			a, ok := address(e.value)
			return ok && a.Is4()
		},
		message: func(f *field, r *rule) string { return "is not a valid IPv4 address" },
	},
	"ipv6": {
		eval: func(e *eval, r *rule) bool {
			a, ok := address(e.value)
			return ok && a.Is6()
		},
		message: func(f *field, r *rule) string { return "is not a valid IPv6 address" },
	},
	"mac_address": {
		eval:    func(e *eval, r *rule) bool { return macShape.MatchString(e.value) },
		message: func(f *field, r *rule) string { return "is not a valid MAC address" },
	},
	"hex_color": {
		eval:    func(e *eval, r *rule) bool { return hexColorShape.MatchString(e.value) },
		message: func(f *field, r *rule) string { return "is not a valid hex color" },
	},
	"timezone": {
		eval: func(e *eval, r *rule) bool {
			// "Local" is a Go spelling of "whatever this machine is set to",
			// which is not a zone a form can mean -- and it would pass on the
			// developer's laptop and on nothing else.
			if e.value == "Local" {
				return false
			}
			_, err := time.LoadLocation(e.value)
			return err == nil
		},
		message: func(f *field, r *rule) string { return "is not a valid time zone" },
	},
}

// acceptable and declinable are Laravel's lists for `accepted` and `declined`,
// minus the values a form can never send: an HTML form carries strings, so the
// booleans and integers in Laravel's arrays are unreachable here.
var (
	acceptable = []string{"yes", "on", "1", "true"}
	declinable = []string{"no", "off", "0", "false"}
)

var (
	// numericShape is PHP's is_numeric, minus the surrounding whitespace it
	// tolerates. strconv.ParseFloat alone would accept "Inf", "NaN" and hex
	// floats, none of which a person types into a form.
	numericShape = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

	// integerShape refuses a leading zero, as PHP's FILTER_VALIDATE_INT does.
	integerShape = regexp.MustCompile(`^[+-]?(0|[1-9][0-9]*)$`)

	digitShape = regexp.MustCompile(`^[0-9]+$`)

	// decimalShape is Laravel's, and it refuses exponent notation on purpose:
	// "1e2" has no decimal places to count.
	decimalShape = regexp.MustCompile(`^[+-]?[0-9]*\.?([0-9]*)$`)

	uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// ulidShape is Crockford base32 without I, L, O and U, and a first
	// character no greater than 7 so the 48-bit timestamp cannot overflow.
	ulidShape = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$`)

	// macShape is the three spellings PHP's FILTER_VALIDATE_MAC accepts.
	macShape = regexp.MustCompile(`^([0-9a-fA-F]{2}[:-]){5}[0-9a-fA-F]{2}$|^([0-9a-fA-F]{4}\.){2}[0-9a-fA-F]{4}$`)

	hexColorShape = regexp.MustCompile(`^#(([0-9a-fA-F]{3}){1,2}|([0-9a-fA-F]{4}){1,2})$`)

	alphaUnicode = regexp.MustCompile(`^[\p{L}\p{M}]+$`)
	alphaASCII   = regexp.MustCompile(`^[a-zA-Z]+$`)
	dashUnicode  = regexp.MustCompile(`^[\p{L}\p{M}\p{N}_-]+$`)
	dashASCII    = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	numUnicode   = regexp.MustCompile(`^[\p{L}\p{M}\p{N}]+$`)
	numASCII     = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
)

// dateLayouts are the layouts `date`, `after` and `before` read when the field
// declares no date_format of its own.
//
// The list is short and stated rather than permissive: Laravel's `date` is
// strtotime, which accepts "next thursday" and "1 fortnight ago", and a rule
// whose accepted set nobody can enumerate cannot be reasoned about. A form that
// needs another spelling declares date_format.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// filled reports a value somebody actually answered. Laravel trims before
// asking, so a box containing three spaces is empty, and "0" is not.
func filled(v string) bool { return strings.TrimSpace(v) != "" }

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func anyAffix(list []string, match func(string, string) bool, v string) bool {
	for _, affix := range list {
		if match(v, affix) {
			return true
		}
	}
	return false
}

// number is PHP's is_numeric for a form value.
func number(v string) (float64, bool) {
	if !numericShape.MatchString(v) {
		return 0, false
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// whole is FILTER_VALIDATE_INT for a form value.
func whole(v string) (int64, bool) {
	if !integerShape.MatchString(v) {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func digitsOnly(v string) bool { return digitShape.MatchString(v) }

// decimalPlaces counts the digits after the point, which is what `decimal`
// bounds. It answers false for anything that is not a plain decimal number.
func decimalPlaces(v string) (int, bool) {
	if _, ok := number(v); !ok {
		return 0, false
	}
	m := decimalShape.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	return len(m[1]), true
}

// emailShape is the shape check `email` and the Email helper share, so there is
// one answer to "is this an address" rather than two that drift.
//
// Whitespace is rejected rather than trimmed: an address with a space in it is
// almost always a paste accident, and silently trimming input hides the mistake
// from the person who made it.
func emailShape(value string) bool {
	if strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	at := strings.IndexByte(value, '@')
	return at > 0 && at != len(value)-1 && strings.Contains(value[at:], ".")
}

// isURL accepts http and https only, and demands a host.
//
// Laravel's list runs to two hundred schemes, including "javascript" through
// Symfony's pattern -- a link a person is about to be sent to is http or it is
// not a link this rule should approve.
func isURL(v string) bool {
	if strings.ContainsAny(v, " \t\r\n") {
		return false
	}
	u, err := url.Parse(v)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// address parses an IP and refuses a zone, which PHP's FILTER_VALIDATE_IP also
// refuses: "fe80::1%eth0" names an interface on one machine.
func address(v string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(v)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a, true
}

func alphabet(r *rule, unicoded, ascii *regexp.Regexp) *regexp.Regexp {
	if len(r.args) == 1 {
		return ascii
	}
	return unicoded
}

// confirmationOf is Laravel's validateConfirmed: the argument names the other
// box, and without one it is this field with _confirmation after it.
func confirmationOf(field string, r *rule) string {
	if len(r.args) == 1 {
		return r.args[0]
	}
	return field + "_confirmation"
}

// parseDate reads a value with the field's declared layout first, then with the
// short list of layouts a form can send.
func parseDate(layout, v string) (time.Time, bool) {
	if layout != "" {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// keywords are the three relative moments after and before accept, so a rule
// that means "not in the past" does not have to be regenerated every morning.
var keywords = map[string]func(time.Time) time.Time{
	"today":     func(now time.Time) time.Time { return startOfDay(now) },
	"tomorrow":  func(now time.Time) time.Time { return startOfDay(now).AddDate(0, 0, 1) },
	"yesterday": func(now time.Time) time.Time { return startOfDay(now).AddDate(0, 0, -1) },
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// compareDates resolves the other moment -- a declared field, a keyword or a
// literal -- and answers what the comparison says. An unreadable value on
// either side fails: a date rule that cannot read the date has not been passed.
func (e *eval) compareDates(r *rule, ok func(int) bool) bool {
	mine, parsed := parseDate(e.f.layout, e.value)
	if !parsed {
		return false
	}
	theirs, parsed := e.moment(r.args[0])
	if !parsed {
		return false
	}
	return ok(mine.Compare(theirs))
}

func (e *eval) moment(arg string) (time.Time, bool) {
	if at, isKeyword := keywords[arg]; isKeyword {
		return at(time.Now()), true
	}
	if other, declared := e.set.byName[arg]; declared {
		return parseDate(other.layout, e.other(arg))
	}
	return parseDate(e.f.layout, arg)
}

// moment renders the argument of after/before for a message.
func moment(arg string) string {
	if _, isKeyword := keywords[arg]; isKeyword {
		return arg
	}
	if _, isDate := parseDate("", arg); isDate {
		return arg
	}
	return Humanize(arg)
}

// measure renders a size limit with its unit. A limit on a string is a number
// of characters; a limit on a field that declares numeric, integer or decimal
// is the number itself, and saying "characters" there is wrong in the one place
// a person is reading carefully.
func measure(f *field, arg string) string {
	if f.numeric {
		return arg
	}
	return arg + " characters"
}

func or(list []string) string  { return joinWith(list, "or") }
func and(list []string) string { return joinWith(list, "and") }

func andHumanized(list []string) string {
	humanized := make([]string, len(list))
	for i, item := range list {
		humanized[i] = Humanize(item)
	}
	return and(humanized)
}

func joinWith(list []string, conjunction string) string {
	switch len(list) {
	case 0:
		return ""
	case 1:
		return list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " " + conjunction + " " + list[len(list)-1]
}
