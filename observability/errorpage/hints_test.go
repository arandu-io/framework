package errorpage

import (
	"strings"
	"testing"
)

// The branch this replaces matched "CSRF", so it matched "CSRFToken" -- and the
// one message reaching this page that carries that word is the view engine
// asking for a CSRFToken() method. Somebody missing a method was told to go edit
// hx-headers on their base template, which is a different failure entirely.
func TestAMissingTokenMethodIsNotAnswearedWithSomebodyElsesFix(t *testing.T) {
	const msg = `view: @csrf needs the page data to provide the token. Add a CSRFToken() string method to PostsIndexData.`

	got, ok := messageHint(msg)
	if ok {
		t.Fatalf("the page answers a missing method with %q", got)
	}
	// And the type name alone must not trip it either.
	if _, ok := messageHint("runtime error: invalid memory address, *security.CSRF"); ok {
		t.Error("naming the CSRF type in a panic produces a hint about hx-headers")
	}
}

// The highest-value case, and the one the page had nothing to say about: the
// migrations have never run against this database.
func TestAMissingTableSaysToMigrate(t *testing.T) {
	for _, msg := range []string{
		`ERROR: relation "posts" does not exist (SQLSTATE 42P01)`,
		`Error 1146 (42S02): Table 'shop.posts' doesn't exist`,
		`no such table: posts`,
	} {
		got, ok := messageHint(msg)
		if !ok {
			t.Errorf("%q produced no hint", msg)
			continue
		}
		if !strings.Contains(got, "aru migrate") {
			t.Errorf("%q: the hint does not name the command that fixes it: %s", msg, got)
		}
		if !strings.Contains(got, `"posts"`) {
			t.Errorf("%q: the hint does not name the relation: %s", msg, got)
		}
	}
}

// An alias typo is in PostgreSQL's same SQLSTATE class, and `aru migrate` is not
// its fix. A framework whose data path is hand-written SQL meets it often.
func TestAnAliasTypoIsNotAMissingMigration(t *testing.T) {
	for _, msg := range []string{
		`missing FROM-clause entry for table "p" (SQLSTATE 42P01)`,
		`Error 1051 (42S02): Unknown table 'shop.posts'`,
	} {
		if got, ok := messageHint(msg); ok {
			t.Errorf("%q was answered with %q", msg, got)
		}
	}
}

// The zero Subject: Authorize refuses before consulting a policy, so the policy
// never ran -- and the person edits it for an hour without effect.
func TestTheZeroSubjectSaysNoPolicyRan(t *testing.T) {
	got, ok := messageHint("security: not authorized: anonymous subject on posts.view")
	if !ok {
		t.Fatal("the zero subject produces no hint, and it is the case where the obvious file is the wrong file")
	}
	if !strings.Contains(got, "security.Guest") {
		t.Errorf("the hint does not name the fix: %s", got)
	}

	// A policy that denied a guest in its own words did run, which is the
	// opposite of what that sentence says.
	if got, ok := messageHint("posts.comment denied for subject 4a1: the anonymous subject on this blog may not comment"); ok {
		t.Errorf("a policy's own denial was reported as no policy having run: %s", got)
	}
}

// At most one sentence, so the page never prints two theories with nothing to
// say which one the framework believes.
func TestAtMostOneMessageHint(t *testing.T) {
	got := hints(viewData{Message: `missing grant, and relation "posts" does not exist`})
	if len(got) != 1 {
		t.Fatalf("the page printed %d theories: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Authorize") {
		t.Errorf("the narrower cause did not win: %s", got[0])
	}
}
