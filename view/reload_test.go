package view

import (
	"strings"
	"testing"
)

// The script must not ask while nobody is looking.
//
// Asking is what replaced holding a connection open -- a held stream spends one
// of the browser's six per origin and is not released on navigation, so a
// handful of clicks froze the page. The only argument against asking is what it
// costs, and these three lines are what makes the cost nothing: a project left
// open in a tab overnight would otherwise be tens of thousands of requests
// nobody wanted.
func TestTheScriptDoesNotAskWhileTheTabIsHidden(t *testing.T) {
	script := string(reloadBody("/_arandu/reload"))

	if !strings.Contains(script, "document.hidden") {
		t.Error("the script asks even when the tab is hidden")
	}
	if !strings.Contains(script, "visibilitychange") {
		t.Error("the script does not ask when you come back, so returning to the tab waits for the next tick")
	}
	if strings.Contains(script, "EventSource") {
		t.Error("the script holds a connection again, which is the defect it was rewritten to remove")
	}
}

// One request at a time. Without the guard, a server answering slowly during a
// restart stacks a new request on every tick, which is the connection
// exhaustion this rewrite exists to prevent, arriving by another road.
func TestTheScriptDoesNotStackRequests(t *testing.T) {
	if !strings.Contains(string(reloadBody("/x")), "checking") {
		t.Error("nothing stops a slow answer from stacking one request per tick")
	}
}

// The address is written as a literal, and a path this binary chose is not the
// case that matters: the case that matters is that nothing here builds markup
// by concatenation.
func TestTheAddressCannotCloseTheScriptElement(t *testing.T) {
	if got := quote("/x</script><script>alert(1)"); strings.Contains(got, "</script>") {
		t.Fatalf("the address closed the element it is written in: %s", got)
	}
}
