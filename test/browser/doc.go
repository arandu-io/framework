// Package browser drives an application in a real browser, the way the person
// using it does, and fails the test on what that person would have seen go
// wrong.
//
// A test opens a Page and talks to it in the user's words:
//
//	func TestInvoiceIsPaid(t *testing.T) {
//		page := browser.Open(t, tests.Drivers, browser.BaseURL(server.URL), browser.Routes(server.Route))
//		page.Visit("invoices.show", invoice.ID)
//		page.ClickRole("button", "Pay")
//		page.ExpectModal("Pay invoice").ClickRole("button", "Confirm")
//		page.ExpectText("Paid")
//		page.Responsive()
//	}
//
// # Locators
//
// Elements are found by role and accessible name (Role), by label (Label), by
// visible text (Text) and by data-testid (TestID). A CSS selector is allowed
// with the reason no other locator can say the same (CSS). Every lookup is
// strict: an action needs exactly one visible element.
//
// # Waiting
//
// There is nothing to wait for by hand. Each action waits for its element to
// be visible, enabled, still and not covered, then acts with the pointer or
// the keyboard, then waits for the page to settle: the document loaded, every
// htmx request settled or ended with nothing to swap, the network idle, the
// animations over. Each expectation retries until it holds or the step's
// timeout passes, and then says what it last saw.
//
// # Failing by itself
//
// An exception the page did not catch, a console.error, a response of 500 or
// more and a Content-Security-Policy violation fail the test at the end of the
// step they happened in, with the cause ahead of whatever expectation it broke.
// Page.Allow accepts the ones a test is about. Page.Responsive walks the
// widths 1440, 768, 390 and 320 in the light and dark schemes and fails when
// the page is wider than the viewport, keeping the geometry of what sticks out.
//
// # Drivers
//
// A Driver is the browser engine: a small interface of what an engine does --
// navigate, evaluate, input, viewport and media, capture, cookies and events.
// Everything else is Page, written once over the interface, so a test runs
// the same on every driver and never calls one driver's API. The default
// driver is browser/cdp, Chrome over its debugging pipe with the standard
// library alone; every driver passes browser/conformance.
//
// The drivers a project can run with are a Registry it writes in one line of
// its test case; ARANDU_E2E_DRIVER picks one and ARANDU_E2E_BROWSER the engine.
// A driver that finds no browser skips the test on a developer's machine and
// fails it in CI.
//
// # Debugging
//
// ARANDU_E2E_HEADED=1 shows the window, ARANDU_E2E_SLOWMO=250ms slows every
// action down, and Page.Pause holds the test with the window open until
// window.__arandu.resume() is called in the page's console. A test runs under
// Delve like any other.
//
// Every step is recorded, and when a test fails the trace -- the steps with a
// PNG and the DOM after each, the network, the console and the failures, in
// the format of package trace -- is written to a directory named after the
// test. ARANDU_E2E_TRACE=1 writes it for passing tests too.
//
// The browser tests of an application carry the e2e build tag, so the
// ordinary go test never starts a browser.
package browser
