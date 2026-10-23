package dbclone

import (
	"errors"
	"strings"
	"testing"
)

// The URL of a clone keeps everything the test named but the database: the
// credentials and the options are what let it connect at all.
func TestURLForReplacesOnlyTheDatabase(t *testing.T) {
	got, err := URLFor("postgres://u:p%40ss@db.local:6543/main?sslmode=require&application_name=x", "arandutest_ab_c01")
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://u:p%40ss@db.local:6543/arandutest_ab_c01?sslmode=require&application_name=x"
	if got != want {
		t.Errorf("URLFor answered %q, want %q", got, want)
	}
}

func TestURLForRefusesAURLWithNoServer(t *testing.T) {
	if _, err := URLFor("sqlite:///tmp/x.sqlite", "arandutest_x"); err == nil {
		t.Error("a URL with no host was accepted as a server to clone on")
	}
}

// Every name this package writes starts with the prefix, and a template and a
// clone of one family never share a name: the sweep of old templates reads
// the "_t" after the family, and must never reach a test's database.
func TestNamesCarryThePrefixAndKeepTemplatesApartFromClones(t *testing.T) {
	family := Key("/pkg", "migrate")
	tpl := TemplateName(family, "0123456789")
	clone, err := CloneName(family)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := CloneName(family)

	for _, name := range []string{tpl, tpl + buildingSuffix, clone} {
		if !strings.HasPrefix(name, Prefix) {
			t.Errorf("%s does not start with %s", name, Prefix)
		}
		if len(name) > 63 {
			t.Errorf("%s is longer than the 63 bytes Postgres keeps of a name", name)
		}
	}
	if strings.HasPrefix(clone, Prefix+family+"_t") {
		t.Errorf("the clone %s falls under the template sweep", clone)
	}
	if clone == other {
		t.Errorf("two clones were both named %s", clone)
	}
}

// The family and the build are separate parts of the name so that two
// migrations of one package never sweep each other's template.
func TestKeyDependsOnEveryPart(t *testing.T) {
	if Key("a", "bc") == Key("ab", "c") {
		t.Error("Key reads the parts as one string, so two families collide")
	}
	if Key("/pkg", "m1") == Key("/pkg", "m2") {
		t.Error("two migrations of one package share a family")
	}
}

func TestInUseReadsTheCodeAndTheSentence(t *testing.T) {
	for _, msg := range []string{
		`ERROR: source database "x" is being accessed by other users (SQLSTATE 55006)`,
		`pq: database "x" is being accessed by other users`,
	} {
		if !InUse(errors.New(msg)) {
			t.Errorf("InUse(%q) = false", msg)
		}
	}
	if InUse(errors.New(`database "x" already exists (SQLSTATE 42P04)`)) || InUse(nil) {
		t.Error("InUse answered true for something else")
	}
}

func TestQuoteDoublesAQuote(t *testing.T) {
	if got := quote(`a"b`); got != `"a""b"` {
		t.Errorf("quote answered %s", got)
	}
}

func TestBuildIsStableWithinAProcess(t *testing.T) {
	first, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Build()
	if first == "" || first != second {
		t.Errorf("Build answered %q then %q", first, second)
	}
}
