package unit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/framework/tests"
)

// noticeFile is the copyright notice every binary built with this framework
// carries an obligation to.
//
// The obligation survives the bytes moving out: github.com/arandu-io/hesape/view
// is what embeds them now, this module requires it, and a binary linked against
// this module therefore redistributes them. What moved out with the bytes is
// the check that reads them -- hesape's view/third_party_test.go asks whether
// the versions recorded beside them are the versions inside them, against the
// files it actually embeds. Asking that here meant reading a copy nobody
// served, which answered for the wrong bytes: an upgrade in hesape left that
// copy untouched, and the question came back green about a notice that had
// stopped being true.
func noticeFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(tests.ModuleRoot(t), "THIRD_PARTY.md")
}

// TestTheLicenseTextsAreComplete: naming a license is not the obligation. MIT
// requires the notice and the permission text to travel with the copy, and a
// file that says "MIT" and stops does not satisfy it.
//
// This one asks nothing of any file but the notice, so it is the whole of what
// this module can still answer for on its own.
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
		t.Fatalf("THIRD_PARTY.md is the copyright notice the redistributed assets require: %v", err)
	}
	return string(body)
}
