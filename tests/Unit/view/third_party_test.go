package unit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arandu-io/framework/tests"
)

// noticeFile is the copyright notice every binary built with this framework
// carries an obligation to, because go:embed makes every user a redistributor.
func noticeFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(tests.ModuleRoot(t), "THIRD_PARTY.md")
}

// assetsDir is the directory whose whole contents are embedded, and therefore
// the list this notice has to cover.
func assetsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(tests.ModuleRoot(t), "view", "assets")
}

// TestEveryEmbeddedAssetIsCredited is the guard that keeps the notice from
// rotting.
//
// A copyright notice nobody maintains is worse than none: it reads as a
// statement of what is in the binary, and it stops being true the first time
// somebody drops a file into view/assets/ -- which is a one-line change nobody
// reviews as a legal one. The repositories are public and the assets ship
// inside every user's binary, so this is the only item in the project with
// actual exposure.
func TestEveryEmbeddedAssetIsCredited(t *testing.T) {
	notice := readNotice(t)

	entries, err := os.ReadDir(assetsDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no assets are embedded, so this test is proving nothing")
	}

	for _, e := range entries {
		if !strings.Contains(notice, e.Name()) {
			t.Errorf("%s is embedded and redistributed in every binary, and THIRD_PARTY.md does not mention it: "+
				"add its name, version, author, license and the full license text", e.Name())
		}
	}
}

// TestTheCreditedVersionsAreTheEmbeddedOnes: an upgrade that leaves the notice
// behind publishes the wrong license for the wrong version, which is the same
// failure as not having the file.
//
// The versions are read out of the shipped bytes rather than out of a list kept
// somewhere else -- there is no second place to update.
func TestTheCreditedVersionsAreTheEmbeddedOnes(t *testing.T) {
	notice := readNotice(t)

	// HTMX exposes its version in its minified build: a `version:"x.y.z"`
	// property on the object it exports.
	declared := regexp.MustCompile(`version:"([0-9][0-9a-zA-Z.\-]*)"`)
	// Tailwind writes a banner instead, and the compiler preserves it.
	banner := regexp.MustCompile(`tailwindcss v([0-9][0-9a-zA-Z.\-]*)`)

	for file, pattern := range map[string]*regexp.Regexp{
		"htmx.min.js": declared,
		"app.css":     banner,
	} {
		body, err := os.ReadFile(filepath.Join(assetsDir(t), file))
		if err != nil {
			t.Fatal(err)
		}
		match := pattern.FindSubmatch(body)
		if match == nil {
			t.Errorf("%s no longer states its version where this test looks: the notice cannot be checked against it", file)
			continue
		}
		if version := string(match[1]); !strings.Contains(notice, version) {
			t.Errorf("%s ships version %s and THIRD_PARTY.md does not record it: update the entry, and confirm the license did not change with the version",
				file, version)
		}
	}
}

// TestTheLicenseTextsAreComplete: naming a license is not the obligation. MIT
// requires the notice and the permission text to travel with the copy, and a
// file that says "MIT" and stops does not satisfy it.
func TestTheLicenseTextsAreComplete(t *testing.T) {
	notice := readNotice(t)

	required := map[string]string{
		"the MIT permission grant": "Permission is hereby granted, free of charge",
		"the MIT notice clause":    "The above copyright notice and this permission notice shall be included",
		"the Tailwind copyright":   "Tailwind Labs, Inc.",
		"the 0BSD grant":           "Permission to use, copy, modify, and/or distribute this software",
	}

	for what, text := range required {
		if !strings.Contains(notice, text) {
			t.Errorf("THIRD_PARTY.md is missing %s: %q", what, text)
		}
	}
}

func readNotice(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(noticeFile(t))
	if err != nil {
		t.Fatalf("THIRD_PARTY.md is the copyright notice the embedded assets require: %v", err)
	}
	return string(body)
}
