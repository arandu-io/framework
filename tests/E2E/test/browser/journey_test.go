//go:build e2e

package e2e

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/framework/test/browser/cdp"
	"github.com/arandu-io/hesape/view"
)

// drivers is what an application writes once in tests/TestCase.go.
var drivers = browser.Registry{"cdp": cdp.Launch}

// invoices is a small application with named routes, an htmx form and a
// confirmation modal, standing in for the server test.Serve starts.
type invoices struct {
	mu   sync.Mutex
	paid map[string]bool
}

func (a *invoices) route(name string, params ...any) (string, error) {
	switch name {
	case "invoices.index":
		return "/invoices", nil
	case "invoices.show":
		return fmt.Sprintf("/invoices/%v", params[0]), nil
	}
	return "", fmt.Errorf("no route named %s", name)
}

func (a *invoices) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(view.AssetPath, view.Handler)
	layout := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "script-src 'self'")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Invoices</title>
<meta name="htmx-config" content='{"includeIndicatorStyles":false}'><script src="%s"></script><script src="/app.js"></script></head><body><main>%s</main></body></html>`, view.Asset("htmx.min.js"), body)
	}
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `document.addEventListener('click', e => {
  const open = e.target.closest('[data-open]');
  if (open) document.getElementById(open.dataset.open).showModal();
});
document.addEventListener('invoice-paid', () => document.getElementById('pay').close());`)
	})
	mux.HandleFunc("GET /invoices", func(w http.ResponseWriter, r *http.Request) {
		layout(w, `<h1>Invoices</h1><a href="/invoices/7">Invoice 7</a>`)
	})
	mux.HandleFunc("GET /invoices/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := html.EscapeString(r.PathValue("id"))
		a.mu.Lock()
		status := "Open"
		if a.paid[id] {
			status = "Paid"
		}
		a.mu.Unlock()
		layout(w, `<h1>Invoice `+id+`</h1><p id="status">`+status+`</p>
<button data-open="pay">Pay</button>
<dialog id="pay" aria-labelledby="pay-title"><h2 id="pay-title">Pay invoice `+id+`</h2>
<form hx-post="/invoices/`+id+`/pay" hx-target="#status" hx-swap="outerHTML">
<label for="card">Card number</label><input id="card" name="card" inputmode="numeric">
<button type="submit">Confirm</button></form></dialog>`)
	})
	mux.HandleFunc("POST /invoices/{id}/pay", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !strings.HasPrefix(r.FormValue("card"), "4242") {
			http.Error(w, "card refused", http.StatusInternalServerError)
			return
		}
		a.mu.Lock()
		a.paid[id] = true
		a.mu.Unlock()
		w.Header().Set("HX-Trigger", "invoice-paid")
		fmt.Fprint(w, `<p id="status">Paid</p>`)
	})
	return mux
}

// TestPayingAnInvoice is a journey written the way an application writes one.
func TestPayingAnInvoice(t *testing.T) {
	app := &invoices{paid: map[string]bool{}}
	server := httptest.NewServer(app.handler())
	t.Cleanup(server.Close)

	page := browser.Open(t, drivers, browser.BaseURL(server.URL), browser.Routes(app.route))
	page.Visit("invoices.index")
	page.ClickText("Invoice 7")
	page.ExpectURL("invoices.show", 7)
	page.ExpectVisible(browser.Role("heading", "Invoice 7"))

	page.ClickRole("button", "Pay")
	modal := page.ExpectModal("Pay invoice 7")
	modal.Find(browser.Label("Card number")).Fill("4242 4242 4242 4242")
	modal.ClickRole("button", "Confirm")
	page.Find(browser.CSS("#status", "the status paragraph has no role or label")).ExpectText("Paid")
	page.Responsive()
}
