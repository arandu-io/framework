package browser

import (
	"encoding/json"
	"strings"
)

// Locator finds an element the way a person does: by its role and accessible
// name, by its label, by its text, or by a test id the markup carries on
// purpose. A CSS selector is the exception, and it says why.
//
// Every lookup is strict: an action needs exactly one visible element, and
// two visible matches are a failure naming both rather than a guess.
//
// Names and text are compared exactly after collapsing whitespace. A label
// whose text ends in the asterisk of a required field matches without it.
type Locator struct {
	kind   string
	a, b   string
	parent *Locator
}

// Role finds an element by its ARIA role, explicit or implied by its tag, and
// its accessible name. An empty name matches any element with the role.
//
//	browser.Role("button", "Save")
//	browser.Role("link", "Invoices")
//	browser.Role("heading", "New invoice")
//
// A text, email, tel, url or password input is a "textbox".
func Role(role, name string) Locator { return Locator{kind: "role", a: role, b: name} }

// Label finds a form control by the text of its label, its aria-labelledby or
// its aria-label.
func Label(text string) Locator { return Locator{kind: "label", a: text} }

// Text finds the innermost element whose visible text is text, or a button
// input whose value is.
func Text(text string) Locator { return Locator{kind: "text", a: text} }

// TestID finds the element whose data-testid is id.
func TestID(id string) Locator { return Locator{kind: "testid", a: id} }

// Modal finds an open modal dialog -- a <dialog> opened with showModal, or an
// element with role dialog or alertdialog and aria-modal="true" -- by its
// accessible name, which falls back to its first heading. An empty name
// matches any modal.
func Modal(name string) Locator { return Locator{kind: "modal", a: name} }

// CSS finds elements by a selector. reason is why no role, label, text or test
// id can say the same; an empty reason fails the step that uses the locator,
// because a class name is styling and styling changes.
func CSS(selector, reason string) Locator {
	return Locator{kind: "css", a: selector, b: strings.TrimSpace(reason)}
}

// In narrows the locator to the inside of the one visible element parent
// finds:
//
//	browser.Role("button", "Delete").In(browser.Modal("Delete invoice"))
func (l Locator) In(parent Locator) Locator {
	p := parent
	l.parent = &p
	return l
}

// String describes the locator the way failures name it.
func (l Locator) String() string {
	var own string
	switch l.kind {
	case "role":
		own = "role=" + l.a
		if l.b != "" {
			own += ` name="` + l.b + `"`
		}
	case "modal":
		own = "modal"
		if l.a != "" {
			own += ` name="` + l.a + `"`
		}
	default:
		own = l.kind + `="` + l.a + `"`
	}
	if l.parent != nil {
		return l.parent.String() + " >> " + own
	}
	return own
}

// problem reports what is wrong with the locator before it reaches the page.
func (l Locator) problem() string {
	for at := &l; at != nil; at = at.parent {
		switch at.kind {
		case "":
			return "the zero Locator finds nothing; build one with Role, Label, Text, TestID, Modal or CSS"
		case "css":
			if at.b == "" {
				return "CSS(" + at.a + `, "") has no reason; a class is styling, so say why no role, label, text or test id can find this element`
			}
		}
	}
	return ""
}

// MarshalJSON encodes the locator for the page script.
func (l Locator) MarshalJSON() ([]byte, error) {
	type wire struct {
		Kind   string   `json:"kind"`
		A      string   `json:"a"`
		B      string   `json:"b,omitempty"`
		Parent *Locator `json:"parent,omitempty"`
	}
	return json.Marshal(wire{Kind: l.kind, A: l.a, B: l.b, Parent: l.parent})
}
