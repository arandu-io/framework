package security

import (
	"strings"
	"testing"
)

// Written from the inside, for the reason throttle_test.go is: localAddress is
// the whole open-redirect defence, and the hostile values it exists to refuse
// cannot all be delivered through a signed cookie from outside the package --
// signing one requires the key. Stating them one per line here is what keeps
// each refusal attached to the shape it refuses.
func TestNothingThatLeavesThisApplicationIsAcceptedAsADestination(t *testing.T) {
	for name, raw := range map[string]string{
		"nowhere at all":                "",
		"another origin":                "https://evil.example/login",
		"a scheme a browser will run":   "javascript:alert(document.cookie)",
		"a protocol-relative address":   "//evil.example/takeover",
		"a backslash in the authority":  "/\\evil.example/takeover",
		"a relative address":            "invoices/42",
		"a newline in a header":         "/invoices\r\nSet-Cookie: a=b",
		"a tab a browser would strip":   "/\t/evil.example",
		"a space a browser would strip": "/ /evil.example",
	} {
		t.Run(name, func(t *testing.T) {
			if to, ok := localAddress(raw); ok {
				t.Fatalf("%q was accepted as %q, and a browser told to go there after a sign-in does not come back", raw, to)
			}
		})
	}

	if _, ok := localAddress("/" + strings.Repeat("a", maxIntendedAddress)); ok {
		t.Error("an address longer than a cookie can hold was accepted, so the browser drops the cookie instead of us deciding not to write it")
	}
}

func TestAnOrdinaryPageAddressIsAccepted(t *testing.T) {
	for _, raw := range []string{
		"/",
		"/invoices",
		"/invoices/42?tab=lines&sort=date",
		"/invoices/42#total",
		"/faturas/n%C3%BAmero",
	} {
		if to, ok := localAddress(raw); !ok || to != raw {
			t.Errorf("%q is a page of this application and was refused, so somebody who followed a link to it signs in and lands on the front page", raw)
		}
	}
}
