package framework_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoBuiltinPageUsesATailwindUtility is the guard for a failure that looks
// like a broken deploy and is invisible in review.
//
// The application stylesheet is compiled with `@import "tailwindcss"
// source(none)` and an explicit `@source` list naming the project's views.
// Markup written inside this module is never on that list -- it lives in the
// module cache of whoever imported the framework -- so a utility class written
// here resolves to nothing at all.
//
// It is worse than unstyled. The component layer (`.btn`, `.field`, `.input`)
// IS emitted unconditionally, so a page mixing the two gets its button styled
// and its layout ignored, and the result reads as a stylesheet that half-loaded.
// That shipped: the sign-in screen ran the full width of the window because
// `max-w-sm` and `justify-center` existed in the HTML and in no stylesheet.
//
// A page the framework has to be able to draw carries its own layout CSS, the
// way observability/errorpage does. Component classes are allowed and are the
// point -- they are what make this screen look like the application around it.
func TestNoBuiltinPageUsesATailwindUtility(t *testing.T) {
	// Utilities, by prefix. Deliberately not exhaustive: it names the families
	// that carry layout, which is what a page here would reach for and what
	// silently does nothing.
	utility := regexp.MustCompile(`\b(` + strings.Join([]string{
		`m[xytblr]?-\d`, `p[xytblr]?-\d`, `gap-\d`, `space-[xy]-\d`,
		`w-\d`, `h-\d`, `min-h-\w+`, `max-w-\w+`, `w-full`, `h-full`,
		`flex`, `grid`, `inline-flex`, `items-\w+`, `justify-\w+`, `flex-col`,
		`text-(xs|sm|base|lg|xl|\dxl)`, `font-(medium|semibold|bold)`,
		`bg-\w+`, `text-\w+-\d`, `border-\w+`, `rounded(-\w+)?`,
		`antialiased`, `tracking-\w+`, `mx-auto`, `shadow(-\w+)?`,
	}, "|") + `)\b`)

	var found []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		// The test files themselves quote markup to assert against, and this one
		// quotes the class names it forbids.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			at := strings.Index(line, `class="`)
			if at < 0 {
				continue
			}
			end := strings.Index(line[at+7:], `"`)
			if end < 0 {
				continue
			}
			for _, class := range strings.Fields(line[at+7 : at+7+end]) {
				if utility.MatchString(class) {
					found = append(found, filepath.ToSlash(path)+":"+itoa(i+1)+" "+class)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range found {
		t.Errorf("%s is a Tailwind utility in framework markup: it is never scanned, "+
			"so it resolves to nothing. Use a component class, or put the rule in this page's own <style>", f)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestBuiltinPageStyleIsInTheBody is the other half of the same failure.
//
// hx-boost swaps the body and the title and discards the rest of the response.
// A <style> block in <head> therefore reaches the browser and is thrown away: the
// page is styled on a direct load and unstyled the moment somebody arrives by
// clicking a link, which is how everybody arrives.
//
// Inside <body> it travels with the swap. Not conformant HTML, honoured by every
// browser for twenty years, and the conformant alternatives are an inline style
// attribute per element or a second stylesheet at a second URL.
func TestBuiltinPageStyleIsInTheBody(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// From the doctype, not from the top of the file. The doc comment above a
		// page explains this rule and names <style> while doing it, and reading
		// the whole file made the explanation itself the violation.
		text := string(body)
		start := strings.Index(text, "<!doctype html>")
		if start < 0 {
			return nil
		}
		text = text[start:]

		// Only pages that opt into boosting are affected. A page without it is
		// always loaded whole, head included.
		bodyAt := strings.Index(text, `<body hx-boost`)
		if bodyAt < 0 {
			return nil
		}
		if strings.Contains(text[:bodyAt], "<style>") {
			t.Errorf("%s has a <style> in <head> on a boosted page: hx-boost keeps the body and the "+
				"title and discards the rest, so those rules never apply when the page is reached by a link. "+
				"Move the block inside <body>", filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
