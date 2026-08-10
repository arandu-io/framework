package arandutest_test

import (
	"net/http"
	"testing"

	"github.com/arandu-io/framework/arandutest"
)

// echoCookie is a handler that reports what the client sent and sets whatever
// the query string asks it to set. It is the smallest thing that can tell a jar
// that keeps a cookie from a jar that merely accumulates them.
func echoCookie(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("set") {
		case "":
		case "clear":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
		default:
			http.SetCookie(w, &http.Cookie{Name: "session", Value: r.URL.Query().Get("set"), Path: "/"})
		}

		if c, err := r.Cookie("session"); err == nil {
			_, _ = w.Write([]byte("carrying " + c.Value))
			return
		}
		_, _ = w.Write([]byte("carrying nothing"))
	})
}

// The property a feature test rests on: after the server clears a cookie, the
// next request does not carry it. A jar that only appends fails this while
// looking like it works, because http.Request.Cookie returns the first value of
// a name and the cleared one is filed behind the live one.
func TestSigningOutIsVisibleToTheNextRequest(t *testing.T) {
	client := arandutest.NewClient(t, echoCookie(t))

	client.Get("/?set=first").See("carrying nothing")
	client.Get("/").See("carrying first")

	client.Get("/?set=clear")
	client.Get("/").See("carrying nothing")
}

// Signing in a second time must replace the session, not hide behind it. When
// it hid, the client went on sending the first id -- which the server had
// deleted when it issued the second -- so every assertion after the second
// sign-in was made by somebody anonymous, and the test read on regardless.
func TestASecondSessionReplacesTheFirstInsteadOfQueueingBehindIt(t *testing.T) {
	client := arandutest.NewClient(t, echoCookie(t))

	client.Get("/?set=first")
	client.Get("/?set=second")

	client.Get("/").See("carrying second").DontSee("carrying first")
}
