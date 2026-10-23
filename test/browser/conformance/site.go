package conformance

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/arandu-io/hesape/view"
)

// site is the application the suite drives: small pages, each proving one
// behaviour, with htmx served from the framework's own embedded copy so the
// waits are proven against the htmx applications run.
func site() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(view.AssetPath, view.Handler)

	page := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-Request-ID", "conformance"+strings.ReplaceAll(r.URL.Path, "/", "-"))
			fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>conformance</title></head><body>%s</body></html>`, body)
		})
	}
	script := func(path, source string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			fmt.Fprint(w, source)
		})
	}
	htmx := `<script src="` + view.Asset("htmx.min.js") + `"></script>`

	page("/", `<h1>Conformance</h1><p>The suite every driver passes.</p>`)

	// htmx: the swap lands late and settles later still; the action must not
	// return before both.
	page("/htmx", htmx+`
<button hx-get="/htmx/slow" hx-target="#out" hx-swap="innerHTML settle:300ms">Load</button>
<div id="out">empty</div>
<button id="self" hx-get="/htmx/self" hx-swap="outerHTML">Replace me</button>
<button hx-post="/htmx/nothing" hx-swap="none">Ping</button>
<p id="pinged"></p>`)
	mux.HandleFunc("/htmx/slow", func(w http.ResponseWriter, r *http.Request) {
		wait(r, 300*time.Millisecond)
		fmt.Fprint(w, `<p class="loaded" title="settled">Loaded</p>`)
	})
	mux.HandleFunc("/htmx/self", func(w http.ResponseWriter, r *http.Request) {
		wait(r, 150*time.Millisecond)
		fmt.Fprint(w, `<button id="replaced">Replaced</button>`)
	})
	mux.HandleFunc("/htmx/nothing", func(w http.ResponseWriter, r *http.Request) {
		wait(r, 150*time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})

	// network idle: the page asks for data after it loaded.
	page("/late", `<p id="api">waiting</p><script src="/late.js"></script>`)
	script("/late.js", `addEventListener('load', () => fetch('/late/data').then(r => r.text()).then(t => { document.getElementById('api').textContent = t; }));`)
	mux.HandleFunc("/late/data", func(w http.ResponseWriter, r *http.Request) {
		wait(r, 300*time.Millisecond)
		fmt.Fprint(w, "arrived")
	})

	// animations: an entrance that runs 400ms.
	page("/animated", `<style>@keyframes enter{from{opacity:0;transform:translateY(40px)}to{opacity:1;transform:none}}#card{animation:enter .4s ease-out}</style><div id="card">Card</div>`)

	// a classic form, answered by a redirect.
	page("/form", `
<style>.styled input{position:absolute;opacity:0} .styled span{display:inline-block;padding:4px;border:1px solid}</style>
<form method="post" action="/form">
<label for="email">Email *</label><input id="email" name="email" type="email" value="old@example.test">
<label>Plan <select name="plan"><option value="basic">Basic</option><option value="pro">Pro</option></select></label>
<label class="styled"><input type="checkbox" name="agree"><span>I agree</span></label>
<label><input type="checkbox" name="news" checked> Newsletter</label>
<label for="note">Note</label><textarea id="note" name="note"></textarea>
<button type="submit">Send</button>
</form>`)
	mux.HandleFunc("POST /form", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := url.Values{}
		for _, name := range []string{"email", "plan", "agree", "news", "note"} {
			q.Set(name, r.Form.Get(name))
		}
		http.Redirect(w, r, "/done?"+q.Encode(), http.StatusSeeOther)
	})
	mux.HandleFunc("/done", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><title>done</title><p>email=%s plan=%s agree=%s news=%s note=%s</p>`,
			html.EscapeString(q.Get("email")), html.EscapeString(q.Get("plan")), html.EscapeString(q.Get("agree")), html.EscapeString(q.Get("news")), html.EscapeString(q.Get("note")))
	})

	// keyboard: every key the page sees, in order.
	page("/keys", `<label for="a">First</label><input id="a"><label for="b">Second</label><input id="b"><p id="log"></p><script src="/keys.js"></script>`)
	script("/keys.js", `document.addEventListener('keydown', e => { document.getElementById('log').textContent += (e.shiftKey ? 'Shift+' : '') + e.key + ' '; });`)

	// a control under an overlay.
	page("/covered", `<button>Hidden behind</button><div id="overlay" style="position:fixed;inset:0;background:rgba(0,0,0,.4)"></div>`)

	// two visible matches for one name.
	page("/twice", `<button>Save</button><button>Save</button><button data-testid="only">Save</button>`)

	// a modal dialog.
	page("/modal", `<button id="open">Delete</button><p id="status"></p>
<dialog aria-labelledby="dialog-title"><h2 id="dialog-title">Delete invoice</h2><button id="confirm">Delete</button></dialog><script src="/modal.js"></script>`)
	script("/modal.js", `const d = document.querySelector('dialog');
document.getElementById('open').addEventListener('click', () => d.showModal());
document.getElementById('confirm').addEventListener('click', () => { d.close(); document.getElementById('status').textContent = 'Deleted'; });`)

	// failures the page must report by itself.
	page("/console", `<p>console</p><script src="/console.js"></script>`)
	script("/console.js", `console.error('conformance console error', 42);`)
	page("/exception", `<p>exception</p><script src="/exception.js"></script>`)
	script("/exception.js", `throw new Error('conformance exception');`)
	page("/server-error", `<p>server error</p><script src="/server-error.js"></script>`)
	script("/server-error.js", `fetch('/boom');`)
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/csp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "script-src 'self'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>csp</title><p>csp</p><script>window.inline = true</script>`)
	})

	// geometry.
	page("/wide", `<main style="padding:8px"><div id="wide" style="width:1000px;height:20px;background:#ccc">wide</div></main>`)
	page("/narrow", `<main style="padding:8px"><div style="max-width:100%;height:20px;background:#ccc">narrow</div></main>`)
	page("/media", `<style>body{background:#fff}@media (prefers-color-scheme: dark){body{background:#000}}</style><p>media</p>`)

	// cookies.
	mux.HandleFunc("/cookie/set", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "conformance", Value: r.URL.Query().Get("v"), Path: "/", HttpOnly: true})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>cookie</title><p>set</p>`)
	})
	mux.HandleFunc("/cookie/echo", func(w http.ResponseWriter, r *http.Request) {
		value := "none"
		if c, err := r.Cookie("conformance"); err == nil {
			value = c.Value
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><title>cookie</title><p>cookie=%s</p>`, html.EscapeString(value))
	})
	return mux
}

// wait holds a response for d, or until the browser gave up on it.
func wait(r *http.Request, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
	}
}
