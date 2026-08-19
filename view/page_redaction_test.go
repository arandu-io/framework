package view_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
)

// liveToken is the value every test below asserts never reaches the output. It
// is distinctive so a substring search cannot match anything else on the page.
const liveToken = "csrf-live-9f3a7c21e5b04d68"

// filled is a page with every field set, so a test asserting that a field
// survives serialization is asserting about a field that had something in it.
func filled() view.Page {
	return view.Page{
		Title:       "New post",
		Description: "Write a post",
		Canonical:   "https://example.test/posts/create",
		AppName:     "Example",
		Token:       liveToken,

		Authenticated: true,
		UserName:      "Ada",

		HomeURL:     "/",
		LoginURL:    "/auth/login",
		LogoutURL:   "/auth/logout",
		RegisterURL: "/auth/register",
		PanelURL:    "/dashboard",
		AdminURL:    "/admin/",

		Path: "/posts/create",

		Errors: validation.Errors{"title": {"is required"}},
		Old:    url.Values{"title": {"A draft"}},
	}
}

// TestMarshalJSONRedactsTheCSRFToken is the whole point of the method: the
// debug page serializes what a dump recorded, and a page carries a token that
// is live for the session reading it.
func TestMarshalJSONRedactsTheCSRFToken(t *testing.T) {
	out, err := json.Marshal(filled())
	if err != nil {
		t.Fatalf("marshalling the page: %v", err)
	}

	if strings.Contains(string(out), liveToken) {
		t.Fatalf("the CSRF token reached the output:\n%s", out)
	}
	if !strings.Contains(string(out), `"Token":"[redacted]"`) {
		t.Fatalf("a filled token must be marked, not dropped:\n%s", out)
	}
}

// TestMarshalJSONLeavesAnEmptyTokenEmpty separates the two states a dump is
// opened to tell apart. A token that was never set must not come back wearing
// the marker, because "the form posted an empty token" is the bug being chased.
func TestMarshalJSONLeavesAnEmptyTokenEmpty(t *testing.T) {
	page := filled()
	page.Token = ""

	out, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshalling the page: %v", err)
	}

	if !strings.Contains(string(out), `"Token":""`) {
		t.Fatalf("an empty token must stay empty:\n%s", out)
	}
	if strings.Contains(string(out), "[redacted]") {
		t.Fatalf("an empty token must not be marked as a secret:\n%s", out)
	}
}

// TestMarshalJSONCarriesEverythingThatIsNotTheToken guards the other direction.
// An allow-list that trims too much answers a dump with a page nobody can read,
// and the fields below are the ones a screen is dumped to look at.
func TestMarshalJSONCarriesEverythingThatIsNotTheToken(t *testing.T) {
	out, err := json.Marshal(filled())
	if err != nil {
		t.Fatalf("marshalling the page: %v", err)
	}

	for _, want := range []string{
		"New post", "Write a post", "https://example.test/posts/create",
		"Example", "Ada", "/auth/login", "/auth/logout", "/auth/register",
		"/dashboard", "/admin/", "/posts/create",
		"is required", "A draft",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the allow-list dropped %q, which is not a secret:\n%s", want, out)
		}
	}
}

// TestLogValueKeepsTheCSRFTokenOutOfLogs covers the second path. Without
// LogValue a handler formats the struct with %+v and writes every field of it,
// the token included, into a log line that is shipped and kept.
func TestLogValueKeepsTheCSRFTokenOutOfLogs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(*bytes.Buffer) slog.Handler
	}{
		{"text", func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) }},
		{"json", func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(tc.handler(&buf)).Info("rendering", "page", filled())

			if strings.Contains(buf.String(), liveToken) {
				t.Fatalf("the CSRF token reached the log:\n%s", buf.String())
			}
			if !strings.Contains(buf.String(), "/posts/create") {
				t.Fatalf("the log line must still say which screen it was:\n%s", buf.String())
			}
		})
	}
}

// TestLogValueDoesNotShipWhatWasTyped keeps the log line to the three fields
// that name the screen. A display name and the contents of a form are for the
// dump, which is one request on one machine, and not for an aggregator.
func TestLogValueDoesNotShipWhatWasTyped(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("rendering", "page", filled())

	for _, unwanted := range []string{"Ada", "A draft", "is required"} {
		if strings.Contains(buf.String(), unwanted) {
			t.Errorf("%q was shipped to the log:\n%s", unwanted, buf.String())
		}
	}
}

// postsIndexData is a screen of the shape the doc comment on Page describes: a
// struct of its own, taking the chrome from the embedded Page.
type postsIndexData struct {
	view.Page
	Posts []string
}

// TestAnEmbeddingScreenInheritsTheRedaction is the case this redaction exists
// for. Nothing inside the framework embeds Page in anything but a test; every
// screen that does lives in a project, and every one of them is covered by the
// method it inherits rather than by a method somebody remembered to write.
func TestAnEmbeddingScreenInheritsTheRedaction(t *testing.T) {
	out, err := json.Marshal(postsIndexData{Page: filled(), Posts: []string{"A draft"}})
	if err != nil {
		t.Fatalf("marshalling the screen: %v", err)
	}

	if strings.Contains(string(out), liveToken) {
		t.Fatalf("the CSRF token reached the output through an embedding screen:\n%s", out)
	}
}

// TestAnEmbeddingScreenSerializesAsTheChromeAlone records the cost of putting
// the method on an embedded type, so that it is a stated behaviour rather than
// something found in a dump that came back short.
//
// A promoted MarshalJSON is the marshaller for the whole struct, so a screen
// that adds fields and does not declare its own method serializes as the chrome
// and nothing else. That is the safe direction to fail -- a field that is
// missing is visible, where a token that is present is not -- and a screen
// dumped for its own data answers it by naming its fields in a method of its
// own.
func TestAnEmbeddingScreenSerializesAsTheChromeAlone(t *testing.T) {
	out, err := json.Marshal(postsIndexData{Page: filled(), Posts: []string{"first", "second"}})
	if err != nil {
		t.Fatalf("marshalling the screen: %v", err)
	}

	if strings.Contains(string(out), "second") {
		t.Fatalf("a promoted MarshalJSON no longer flattens the screen; the redaction may have moved:\n%s", out)
	}
}
