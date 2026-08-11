package validation

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

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
// and the file -- so a rule set that boots is a rule set whose names are all
// real, whose regular expressions compile, whose numeric arguments are numbers
// and whose cross-field references name fields that exist.
type Rules map[string]string

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
type Messages map[string]string

// Option adjusts a compilation.
type Option func(*settings)

type settings struct {
	messages Messages
}

// WithMessages replaces the default sentence for the named field and rule.
func WithMessages(m Messages) Option {
	return func(s *settings) { s.messages = m }
}

// CompileError is one thing wrong with a rule set, named precisely enough to
// fix without opening the framework.
type CompileError struct {
	// Field is the input the rule was written against.
	Field string
	// Rule is the rule name, or empty when the failure is about the field as a
	// whole.
	Rule string
	// File and Line are where the rule set was compiled, from runtime.Caller --
	// so the message names the application's source, not this package's.
	File string
	Line int
	// Msg says what is wrong and, where there is one, where the answer lives.
	Msg string
}

// Error renders one failure on its own, with the file and line, because a
// single failure has to stand alone when it is the only one.
func (e CompileError) Error() string {
	return fmt.Sprintf("validation: rule set at %s:%d: %s", e.File, e.Line, e.short())
}

func (e CompileError) short() string { return fmt.Sprintf("field %q: %s", e.Field, e.Msg) }

// CompileErrors is everything wrong with a rule set, not the first thing.
//
// A set with three typos reports three. Reporting one at a time turns a boot
// check into three restarts, which is how a boot check gets a reputation for
// being slower than finding out on the request.
type CompileErrors []CompileError

// Error renders the header once and one failure per line, the way kyse.Errors
// does, so a panic reads as a list rather than as a paragraph.
func (e CompileErrors) Error() string {
	if len(e) == 0 {
		return "validation: rule set is not valid"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "validation: rule set at %s:%d", e[0].File, e[0].Line)
	for _, err := range e {
		b.WriteString("\n  ")
		b.WriteString(err.short())
	}
	return b.String()
}

// Compile parses and checks a rule set, and reports everything wrong with it.
//
// Use it where a rule set is built from something that is not a literal.
// Everywhere else the rule set belongs in a package-level variable, which is
// what MustCompile is for.
func Compile(r Rules, opts ...Option) (*Set, error) {
	file, line := caller()
	return compile(r, file, line, opts)
}

// MustCompile is Compile for a package-level variable, which is where a rule
// set belongs: the check then runs before main does.
//
//	var StorePost = validation.MustCompile(validation.Rules{
//		"title": "required|max:255",
//	})
//
// It panics, for the reason view.Register panics: finding out at boot beats
// finding out from the one request that first exercises the rule. A set
// compiled inside a handler is checked once per request and reports its typo to
// whoever triggered it -- `aru doctor` refuses that with rules-not-at-boot,
// because this package cannot see where it was called from.
func MustCompile(r Rules, opts ...Option) *Set {
	file, line := caller()
	s, err := compile(r, file, line, opts)
	if err != nil {
		panic(err)
	}
	return s
}

// caller reports the application source that asked for the rule set. The skip
// counts caller itself and its one exported caller.
func caller() (string, int) {
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		return "?", 0
	}
	return file, line
}

func compile(r Rules, file string, line int, opts []Option) (*Set, error) {
	var cfg settings
	for _, opt := range opts {
		opt(&cfg)
	}

	s := &Set{
		byName:   make(map[string]*field, len(r)),
		messages: cfg.messages,
		file:     file,
		line:     line,
	}
	c := &compiler{set: s, file: file, line: line}

	// Fields in sorted order and rules in written order, so the failures come
	// out the same way on every run and a golden file does not churn.
	for _, name := range slices.Sorted(maps.Keys(r)) {
		f := c.parse(name, r[name])
		s.fields = append(s.fields, f)
		s.byName[name] = f
	}
	for _, f := range s.fields {
		c.flags(f)
	}
	for _, f := range s.fields {
		c.checkField(f)
	}
	c.checkMessages()

	if len(c.errs) > 0 {
		// Failures are collected in three passes -- every field is parsed
		// before any check runs, so that a cross-field reference can be
		// resolved -- and are put back in field order here. Stable, so the
		// order within a field stays the order it was written in: a name
		// nobody recognises before a combination nothing can pass.
		slices.SortStableFunc(c.errs, func(a, b CompileError) int {
			return strings.Compare(a.Field, b.Field)
		})
		return nil, c.errs
	}
	return s, nil
}

type compiler struct {
	set  *Set
	file string
	line int
	errs CompileErrors
}

func (c *compiler) fail(f *field, ruleName, format string, args ...any) {
	c.errs = append(c.errs, CompileError{
		Field: f.name,
		Rule:  ruleName,
		File:  c.file,
		Line:  c.line,
		Msg:   fmt.Sprintf(format, args...),
	})
}

// parse turns one chain into rules, reporting the failures that are visible
// from the text alone: an unknown or refused name, a name that is not lowercase,
// a wrong number of arguments, a malformed list, a rule written twice.
func (c *compiler) parse(name, chain string) *field {
	f := &field{name: name}
	if strings.TrimSpace(name) == "" {
		c.fail(f, "", "a rule set cannot key a chain on an empty name")
		return f
	}
	if strings.TrimSpace(chain) == "" {
		c.fail(f, "", "has no rules -- remove the entry, or give it one")
		return f
	}

	seen := make(map[string]bool)
	for _, segment := range splitChain(chain) {
		ruleName, rest, hasArgs := strings.Cut(segment, ":")
		ruleName = strings.TrimSpace(ruleName)

		sp, ok := specs[ruleName]
		if !ok {
			c.reject(f, ruleName)
			continue
		}
		if seen[ruleName] {
			c.fail(f, ruleName, "rule %q is written twice -- the second says nothing the first does not", ruleName)
			continue
		}
		seen[ruleName] = true

		args, err := parseArgs(ruleName, rest, hasArgs)
		if err != nil {
			c.fail(f, ruleName, "rule %q has a malformed argument list: %s", ruleName, err)
			continue
		}
		if hasArgs && sp.maxArgs == 0 {
			c.fail(f, ruleName, "rule %q takes no argument, got %q", ruleName, rest)
			continue
		}
		if len(args) < sp.minArgs {
			c.fail(f, ruleName, "rule %q needs %s, got %d", ruleName, plural(sp.minArgs, "argument"), len(args))
			continue
		}
		if sp.maxArgs > 0 && len(args) > sp.maxArgs {
			c.fail(f, ruleName, "rule %q takes at most %s, got %d", ruleName, plural(sp.maxArgs, "argument"), len(args))
			continue
		}
		f.rules = append(f.rules, &rule{name: ruleName, args: args, spec: sp})
	}
	return f
}

// reject answers a name that is not in the catalogue: a rule this package
// refuses on purpose says where the answer lives, and anything else is a typo
// and gets the near miss.
func (c *compiler) reject(f *field, name string) {
	if because, deliberate := refused[name]; deliberate {
		c.fail(f, name, "rule %q %s", name, because)
		return
	}
	if lower := strings.ToLower(name); lower != name {
		if _, known := specs[lower]; known {
			c.fail(f, name, "rule %q is not spelled that way -- rules are lowercase: %q", name, lower)
			return
		}
	}
	if near := suggest(name); near != "" {
		c.fail(f, name, "unknown rule %q -- did you mean %s?", name, near)
		return
	}
	c.fail(f, name, "unknown rule %q", name)
}

// flags reads the properties of a field that its own rules decide: whether a
// size is measured in characters or by value, whether the field is optional,
// where it stops, and what layout its dates are written in.
func (c *compiler) flags(f *field) {
	for _, r := range f.rules {
		switch {
		case r.name == "bail":
			f.bail = true
		case r.name == "sometimes":
			f.sometimes = true
		case r.name == "date_format" && len(r.args) == 1:
			f.layout = r.args[0]
		}
		if r.spec.sizeIsValue {
			f.numeric = true
		}
	}
}

func (c *compiler) checkField(f *field) {
	for _, r := range f.rules {
		for _, i := range r.spec.refs {
			if i >= len(r.args) {
				continue
			}
			if _, declared := c.set.byName[r.args[i]]; declared {
				continue
			}
			if _, isNumber := number(r.args[i]); isNumber && comparisons[r.name] {
				// Laravel accepts gt:10 as well as gt:other_field, and decides
				// which was meant by whether a field called "10" happens to
				// exist. That is an ambiguity, not a feature, and there is
				// already one way to write a literal bound.
				c.fail(f, r.name, "rule %q takes the name of another field, and %q is a number -- "+
					"a literal bound is written with min: or max:", r.name, r.args[i])
				continue
			}
			// The check that pays for itself: `confirmed` against a field name
			// that does not exist is Laravel's most common silent pass. The
			// rule compares against the empty string for ever and nothing
			// anywhere says so.
			c.fail(f, r.name, "rule %q points at %q, which this rule set does not declare",
				r.name, r.args[i])
		}
		if r.spec.check == nil {
			continue
		}
		if err := r.spec.check(&checkCtx{set: c.set, f: f, r: r}); err != nil {
			c.fail(f, r.name, "%s", err)
		}
	}
	c.checkCombinations(f)
}

// comparisons are the four rules whose argument is a field name and could be
// mistaken for a bound.
var comparisons = map[string]bool{"gt": true, "gte": true, "lt": true, "lte": true}

// incompatible are the pairs that cannot both hold. The table is closed and
// short on purpose: it catches the copy-paste that merged two chains, not every
// arrangement of rules somebody could argue about.
var incompatible = []struct{ a, b string }{
	{"required", "prohibited"},
	{"required", "missing"},
	{"present", "missing"},
	{"integer", "alpha"},
	{"numeric", "boolean"},
	{"lowercase", "uppercase"},
}

func (c *compiler) checkCombinations(f *field) {
	for _, pair := range incompatible {
		if f.rule(pair.a) != nil && f.rule(pair.b) != nil {
			c.fail(f, pair.a, "rules %q and %q cannot both hold", pair.a, pair.b)
		}
	}

	if lo, hi := f.rule("min"), f.rule("max"); lo != nil && hi != nil &&
		len(lo.nums) == 1 && len(hi.nums) == 1 && lo.nums[0] > hi.nums[0] {
		c.fail(f, "min", "min:%s is above max:%s, so nothing can pass", lo.args[0], hi.args[0])
	}

	if in, out := f.rule("in"), f.rule("not_in"); in != nil && out != nil {
		for _, v := range in.args {
			if contains(out.args, v) {
				c.fail(f, "in", "%q is in both in: and not_in:, so nothing can pass", v)
				break
			}
		}
	}

	if f.rule("regex") != nil && f.rule("not_regex") != nil {
		c.fail(f, "regex", "%q and %q cannot both appear -- a pattern runs to the end of the "+
			"chain, so only one of them fits. A set that needs both writes the second in Go, "+
			"in the arandu:begin custom block", "regex", "not_regex")
	}
	for _, name := range []string{"regex", "not_regex"} {
		r := f.rule(name)
		if r == nil || len(r.args) != 1 {
			continue
		}
		if tail := looksLikeARule(r.args[0]); tail != "" {
			c.fail(f, name, "rule %q must be last in the chain -- its pattern runs to the end "+
				"of the string, so this one is %q and %q never runs. Move it to the end; a "+
				"pattern that really means to alternate on %q writes it as a group",
				name, r.args[0], tail, tail)
		}
	}
}

// looksLikeARule reports the rule name a regular expression seems to have
// swallowed, and empty when it has not.
//
// The pattern runs to the end of the chain, so "regex:^a$|max:10" compiles the
// pattern "^a$|max:10" and max never runs -- silently, and forever. There is no
// way to tell that from a pattern that genuinely alternates, so the check is
// this: an alternative that is exactly a known rule name, or a known rule name
// followed by a colon, is a chain somebody wrote in the wrong order.
//
// It can be wrong. A pattern that really means to match the literal text "url"
// as an alternative writes "(url)" or "[u]rl", and the message says so.
func looksLikeARule(pattern string) string {
	parts := strings.Split(pattern, "|")
	for _, part := range parts[1:] {
		name, _, _ := strings.Cut(part, ":")
		if _, known := specs[name]; known {
			return part
		}
		if _, deliberate := refused[name]; deliberate {
			return part
		}
	}
	return ""
}

func (c *compiler) checkMessages() {
	for _, key := range slices.Sorted(maps.Keys(c.set.messages)) {
		fieldName, ruleName, ok := strings.Cut(key, ".")
		f := c.set.byName[fieldName]
		if !ok || f == nil {
			c.errs = append(c.errs, CompileError{
				Field: fieldName, File: c.file, Line: c.line,
				Msg: fmt.Sprintf("a message is keyed %q, and this rule set declares no such field. "+
					"The key is \"field.rule\"", key),
			})
			continue
		}
		if f.rule(ruleName) == nil {
			c.fail(f, ruleName, "a message is keyed %q, and %q declares no rule %q", key, fieldName, ruleName)
		}
	}
}

// splitChain splits a chain on "|", except that a regular expression takes
// everything to the end of it.
//
// Laravel's answer to a pipe inside a pattern is that you cannot use the string
// form at all: it splits on "|" unconditionally and the documented fix is to
// pass the rules as an array. An array form here would be a second spelling of
// a rule set (RULE 9), so the answer is position instead -- regex: and
// not_regex: run to the end, at most one may appear, and it must be last.
//
// The pattern is taken verbatim. There is no escape character, which matters:
// `\|` in a Go regular expression already means a literal pipe, so unescaping
// it would silently turn a literal into an alternation.
func splitChain(chain string) []string {
	var segments []string
	rest := chain
	for {
		head := rest
		if i := strings.IndexByte(rest, '|'); i >= 0 {
			head = rest[:i]
		}
		if name, _, hasArgs := strings.Cut(head, ":"); hasArgs && (name == "regex" || name == "not_regex") {
			return append(segments, rest)
		}
		segments = append(segments, head)
		if len(head) == len(rest) {
			return segments
		}
		rest = rest[len(head)+1:]
	}
}

// parseArgs reads the argument list of one rule.
//
// It is one RFC 4180 record, which is Laravel's str_getcsv: `in:"a,b",c` is two
// values, `a,b` and `c`. A regular expression is not a list and is taken whole.
func parseArgs(name, rest string, hasArgs bool) ([]string, error) {
	if !hasArgs || rest == "" {
		return nil, nil
	}
	if name == "regex" || name == "not_regex" {
		return []string{rest}, nil
	}
	r := csv.NewReader(strings.NewReader(rest))
	r.FieldsPerRecord = -1
	record, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// checkCtx is what a rule's boot check gets: the rule, the field whose flags
// are already known, and the set, so a cross-field argument can be resolved.
type checkCtx struct {
	set *Set
	f   *field
	r   *rule
}

func chain(checks ...func(*checkCtx) error) func(*checkCtx) error {
	return func(c *checkCtx) error {
		for _, check := range checks {
			if err := check(c); err != nil {
				return err
			}
		}
		return nil
	}
}

// needSizes parses a size limit and keeps it on the rule, so a request never
// parses "255" again.
//
// A limit on a string is a count of characters and must be whole; a limit on a
// field that declares numeric, integer or decimal is a bound on the value and
// may have a fraction.
func needSizes(c *checkCtx) error {
	for _, a := range c.r.args {
		n, ok := number(a)
		if !ok {
			if c.f.numeric {
				return fmt.Errorf("rule %q needs a number, got %q", c.r.name, a)
			}
			return fmt.Errorf("rule %q needs a whole number, got %q", c.r.name, a)
		}
		if n < 0 {
			return fmt.Errorf("rule %q needs a number that is not negative, got %q -- a bound "+
				"below zero is written in Go, in the arandu:begin custom block", c.r.name, a)
		}
		if !c.f.numeric && n != float64(int64(n)) {
			return fmt.Errorf("rule %q counts characters here, so it needs a whole number, got %q "+
				"-- declare numeric, integer or decimal to bound the value instead", c.r.name, a)
		}
		c.r.nums = append(c.r.nums, n)
	}
	return nil
}

func needWholeNumbers(c *checkCtx) error {
	for _, a := range c.r.args {
		n, ok := whole(a)
		if !ok {
			return fmt.Errorf("rule %q needs a whole number, got %q", c.r.name, a)
		}
		if n < 0 {
			return fmt.Errorf("rule %q needs a whole number that is not negative, got %q", c.r.name, a)
		}
		c.r.nums = append(c.r.nums, float64(n))
	}
	return nil
}

func ascending(c *checkCtx) error {
	if len(c.r.nums) == 2 && c.r.nums[0] > c.r.nums[1] {
		return fmt.Errorf("rule %q has a low bound above its high bound (%s then %s), so nothing can pass",
			c.r.name, c.r.args[0], c.r.args[1])
	}
	return nil
}

func checkAscii(c *checkCtx) error {
	if len(c.r.args) == 1 && c.r.args[0] != "ascii" {
		return fmt.Errorf("rule %q takes only %q as an argument, got %q", c.r.name, "ascii", c.r.args[0])
	}
	return nil
}

func refuseEmailArguments(c *checkCtx) error {
	if len(c.r.args) == 0 {
		return nil
	}
	return fmt.Errorf("rule %q takes no argument, got %q. Laravel's arguments to it -- dns, spoof, "+
		"rfc -- put a network lookup on the request path, with a timeout nobody set, answering a "+
		"question that is not the one that matters: an address that resolves is not an address "+
		"that receives", c.r.name, strings.Join(c.r.args, ","))
}

func compilePattern(c *checkCtx) error {
	re, err := regexp.Compile(c.r.args[0])
	if err != nil {
		// Laravel never compiles a pattern until a request touches it, so an
		// unclosed group is a 500 on the first form somebody submits. Here it
		// is a boot failure naming the field.
		return fmt.Errorf("rule %q has a pattern that does not compile: %s", c.r.name, err)
	}
	c.r.re = re
	return nil
}

// theLayout is the layout suggested when a Laravel one is written by mistake.
const theLayout = "2006-01-02"

// checkLayout refuses a PHP date format.
//
// date_format takes a GO layout: date_format:2006-01-02, never date_format:Y-m-d.
// It is the one Laravel string that cannot be copied, because the string means
// something else in the host language -- the same reason a kyse template ends
// in .go.
//
// The round trip alone does not catch it: time.Now().Format("Y-m-d") returns
// "Y-m-d" and time.Parse("Y-m-d", "Y-m-d") succeeds, so the layout would boot
// and then reject every date anybody ever sent. Two different instants that
// format identically is what proves the layout contains no date at all.
func checkLayout(c *checkCtx) error {
	layout := c.r.args[0]
	one := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	two := time.Date(2011, 11, 12, 3, 45, 6, 0, time.UTC)
	if one.Format(layout) == two.Format(layout) {
		return fmt.Errorf("rule %q needs a Go layout and %q is not one -- it renders the same text "+
			"for every instant. A Go layout spells the date out: %q, never %q",
			c.r.name, layout, theLayout, "Y-m-d")
	}
	rendered := one.Format(layout)
	parsed, err := time.Parse(layout, rendered)
	if err != nil {
		return fmt.Errorf("rule %q has a layout that cannot read what it writes: %q renders %q, "+
			"which does not parse back (%s)", c.r.name, layout, rendered, err)
	}
	if parsed.Format(layout) != rendered {
		return fmt.Errorf("rule %q has a layout that does not round-trip: %q renders %q and reads "+
			"it back as %q", c.r.name, layout, rendered, parsed.Format(layout))
	}
	return nil
}

// checkMoment refuses an after:/before: argument that is neither a moment nor a
// field, which would otherwise compare against a date that never parses and so
// fail every value forever.
func checkMoment(c *checkCtx) error {
	arg := c.r.args[0]
	if _, isKeyword := keywords[arg]; isKeyword {
		return nil
	}
	if _, declared := c.set.byName[arg]; declared {
		return nil
	}
	if _, isDate := parseDate(c.f.layout, arg); isDate {
		return nil
	}
	return fmt.Errorf("rule %q needs a moment and %q is not one: write a field this rule set "+
		"declares, one of today, tomorrow or yesterday, or a date this field can read (%s)",
		c.r.name, arg, theLayout)
}

// suggest names the rules within a typo's reach.
//
// kyse suggests on a two-character prefix, which works over sixteen directives;
// over sixty-one rule names it returns a third of the table, and "required" and
// "regex" would be offered for the same mistake. Edit distance answers the
// question actually being asked -- "requried" is two edits from "required" and
// nothing else is close.
func suggest(name string) string {
	if name == "" {
		return ""
	}
	limit := min(3, max(1, len(name)/2))
	best := limit + 1
	var near []string
	for candidate := range specs {
		d := distance(name, candidate)
		switch {
		case d < best:
			best, near = d, []string{candidate}
		case d == best:
			near = append(near, candidate)
		}
	}
	if best > limit {
		return ""
	}
	slices.Sort(near)
	return quoteAll(near)
}

func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return or(quoted)
}

// distance is Levenshtein over two short ASCII names, on one row of state.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
