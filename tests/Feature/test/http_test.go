package feature

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/framework/test"
	"github.com/arandu-io/hesape/auth"
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
	client := test.NewClient(t, echoCookie(t))

	client.Get("/?set=first").AssertSee("carrying nothing")
	client.Get("/").AssertSee("carrying first")

	client.Get("/?set=clear")
	client.Get("/").AssertSee("carrying nothing")
}

// Signing in a second time must replace the session, not hide behind it. When
// it hid, the client went on sending the first id -- which the server had
// deleted when it issued the second -- so every assertion after the second
// sign-in was made by somebody anonymous, and the test read on regardless.
func TestASecondSessionReplacesTheFirstInsteadOfQueueingBehindIt(t *testing.T) {
	client := test.NewClient(t, echoCookie(t))

	client.Get("/?set=first")
	client.Get("/?set=second")

	client.Get("/").AssertSee("carrying second").AssertDontSee("carrying first")
}

// whoami answers with what a policy would see: the subject the request carries,
// read the way every policy reads it.
func whoami(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := auth.SubjectFrom(r.Context())
		if !ok {
			_, _ = w.Write([]byte("nobody loaded a session"))
			return
		}
		if s.IsGuest() {
			_, _ = w.Write([]byte("guest of " + s.Tenant))
			return
		}
		_, _ = w.Write([]byte("subject " + s.ID + " of " + s.Tenant))
	})
}

// The one this package exists for. ActingAs has to put the subject where the
// framework reads it -- auth.SubjectFrom, the same call every policy makes --
// and not in a key of its own, which is what it used to do: a context value
// nothing read, so the handler stayed anonymous and the test said it was
// somebody.
func TestActingAsIsVisibleWhereAPolicyLooks(t *testing.T) {
	client := test.NewClient(t, whoami(t))

	client.Get("/").AssertSee("nobody loaded a session")

	client.ActingAs(auth.Subject{ID: "u1", Tenant: "acme"})
	client.Get("/").AssertSee("subject u1 of acme")
}

// It holds for every request afterwards, not just the next one. A client that
// forgot after the first call is the sign-in that has to be repeated before
// each assertion, and the test that stops doing it is anonymous without saying
// so.
func TestActingAsHoldsForEveryLaterRequest(t *testing.T) {
	client := test.NewClient(t, whoami(t)).ActingAs(auth.Subject{ID: "u1", Tenant: "acme"})

	client.Get("/").AssertSee("subject u1 of acme")
	client.Get("/again").AssertSee("subject u1 of acme")
}

// A declared anonymous reader is a subject too, and it is not the same fact as
// a request that never loaded a session: Authorize refuses the second and lets
// a policy decide about the first.
func TestActingAsAcceptsADeclaredGuest(t *testing.T) {
	client := test.NewClient(t, whoami(t)).ActingAs(auth.Guest("acme"))

	client.Get("/").AssertSee("guest of acme")
}

// tokenPage is a page carrying the hidden field a form carries, so a client
// that loads it has a token to send afterwards.
const tokenPage = `<form method="post"><input type="hidden" name="_token" value="tok-1"></form>`

// tokenOf reads the token off a request the way a guard reads it: the header
// first, then the parsed form. The order is not a preference -- net/http parses
// a body into the form only for POST, PUT and PATCH, so on a DELETE the header
// is the only place a token can be.
func tokenOf(r *http.Request) string {
	if token := r.Header.Get("X-CSRF-Token"); token != "" {
		return token
	}
	_ = r.ParseForm()
	return r.PostFormValue("_token")
}

// echoRequest hands out the token page on a GET and, on anything else, answers
// with the method it was asked with and the token the request carried -- the
// pair every verb has to get right.
func echoRequest(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(tokenPage))
			return
		}
		token := tokenOf(r)
		if token == "" {
			token = "none"
		}
		_, _ = w.Write([]byte(r.Method + " with token " + token))
	})
}

// A generated resource registers PUT, PATCH and DELETE beside GET and POST. A
// client that reaches only two of the five leaves three routes of every
// generated module untestable, and a test that posts to a route registered as a
// DELETE proves something about a route the application does not have.
func TestEveryVerbAResourceRegistersReachesTheHandler(t *testing.T) {
	for _, c := range []struct {
		name string
		send func(*test.Client) *test.Response
		want string
	}{
		{"put", func(cl *test.Client) *test.Response {
			return cl.Put("/invoices/1", map[string]string{"total": "10"})
		}, "PUT with token tok-1"},
		{"patch", func(cl *test.Client) *test.Response {
			return cl.Patch("/invoices/1", map[string]string{"total": "10"})
		}, "PATCH with token tok-1"},
		{"delete", func(cl *test.Client) *test.Response {
			return cl.Delete("/invoices/1", nil)
		}, "DELETE with token tok-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := test.NewClient(t, echoRequest(t))
			client.Get("/invoices/1/edit")

			c.send(client).AssertOk().AssertSee(c.want)
		})
	}
}

// A delete has no form to hide a field in, and net/http never parses a DELETE
// body into one. The token therefore has to arrive in the header, which is what
// the generated markup does with hx-headers: a client that sent it only in the
// body would arrive at the guard carrying nothing, and the only way to make the
// test pass would be to turn the guard off.
func TestADeleteCarriesTheTokenWhereAGuardCanReadIt(t *testing.T) {
	client := test.NewClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(tokenPage))
			return
		}
		_ = r.ParseForm()
		_, _ = w.Write([]byte("header " + r.Header.Get("X-CSRF-Token") +
			", form " + r.PostFormValue("_token")))
	}))

	client.Get("/invoices/1")
	client.Delete("/invoices/1", nil).AssertSee("header tok-1, form ")
}

// An OPTIONS asks what may be done with the address, so it sends no body: a
// request that arrives with a form is a request that did something.
func TestOptionsAsksWithoutSendingABody(t *testing.T) {
	client := test.NewClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Allow", "GET, DELETE")
		_, _ = w.Write([]byte(r.Method + " carried " + strconv.Itoa(len(body)) + " bytes"))
	}))

	client.Get("/invoices/1")
	response := client.Options("/invoices/1")

	response.AssertSee("OPTIONS carried 0 bytes")
	if got := response.Header("Allow"); got != "GET, DELETE" {
		t.Errorf("Allow came back %q, want %q", got, "GET, DELETE")
	}
}

// The jar holds across the verbs too. It used to hold only across the two that
// existed, which is the same thing said about a smaller set: a sign-in followed
// by a delete has to arrive as the same session or the delete is anonymous.
func TestTheJarSurvivesEveryVerb(t *testing.T) {
	client := test.NewClient(t, echoCookie(t))

	client.Get("/?set=first")
	client.Put("/", nil).AssertSee("carrying first")
	client.Patch("/", nil).AssertSee("carrying first")
	client.Delete("/", nil).AssertSee("carrying first")
	client.Options("/").AssertSee("carrying first")
}

// echoHeader answers with one header's value, or with the word for its absence.
func echoHeader(t *testing.T, name string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get(name)
		if value == "" {
			value = "nothing"
		}
		_, _ = w.Write([]byte(name + " is " + value))
	})
}

// A header set on the client is sent by every request afterwards, not just the
// next one: a page that boosts fires HX-Request on everything it sends, and a
// seam that held for one call would describe a client nobody has.
func TestAHeaderHoldsForEveryLaterRequest(t *testing.T) {
	client := test.NewClient(t, echoHeader(t, "HX-Request"))

	client.Get("/").AssertSee("HX-Request is nothing")

	client.WithHeader("HX-Request", "true")
	client.Get("/").AssertSee("HX-Request is true")
	client.Post("/", nil).AssertSee("HX-Request is true")
	client.Delete("/", nil).AssertSee("HX-Request is true")
}

// Saying a request is not an HTMX one after saying the others were is the other
// half of the seam. Without it a test would have to build a second client to
// ask the same handler the same question the other way.
func TestAnEmptyValueRemovesTheHeader(t *testing.T) {
	client := test.NewClient(t, echoHeader(t, "HX-Request")).WithHeader("HX-Request", "true")

	client.Get("/").AssertSee("HX-Request is true")

	client.WithHeader("HX-Request", "")
	client.Get("/").AssertSee("HX-Request is nothing")
}

// The client reads a token off the last page and sends it. A test that wants to
// prove the guard refuses somebody else's token has to be able to send somebody
// else's, so what the test sets replaces what the client inferred.
func TestAHeaderTheTestSetsReplacesTheOneTheClientInferred(t *testing.T) {
	client := test.NewClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(tokenPage))
			return
		}
		_, _ = w.Write([]byte("guard read " + r.Header.Get("X-CSRF-Token")))
	}))

	client.Get("/invoices/1")
	client.Delete("/invoices/1", nil).AssertSee("guard read tok-1")

	client.WithHeader("X-CSRF-Token", "stolen")
	client.Delete("/invoices/1", nil).AssertSee("guard read stolen")
}

// The path the generator emits, end to end: hx-delete on a button, the token in
// hx-headers because there is no form to hide it in, and a handler that answers
// an HTMX request with HX-Redirect and no body. AssertRedirect could already
// read that header; until now nothing could provoke it.
func TestTheHTMXDeleteThePayloadGeneratorEmitsIsReachable(t *testing.T) {
	client := test.NewClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(tokenPage))
			return
		}
		if r.Method != http.MethodDelete || r.Header.Get("HX-Request") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-CSRF-Token") != "tok-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("HX-Redirect", "/invoices")
		w.WriteHeader(http.StatusNoContent)
	}))

	client.Get("/invoices/1")
	client.WithHeader("HX-Request", "true").
		Delete("/invoices/1", nil).
		AssertStatus(http.StatusNoContent).
		AssertRedirect("/invoices")
}

// A form value is URL-encoded on the way out. Joined by hand, a password with
// an ampersand arrived cut in two, a plus arrived as a space and a percent sign
// broke the whole body, so a test could not type the input it was about.
func TestAFormValueArrivesExactlyAsTyped(t *testing.T) {
	const typed = "a&b=c+d%e f/ã"
	client := test.NewClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(tokenPage))
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("password=[" + r.PostFormValue("password") + "] token=[" +
			r.PostFormValue("_token") + "] fields=" + strconv.Itoa(len(r.PostForm))))
	}))

	client.Get("/sign-in")
	client.Post("/sign-in", map[string]string{"password": typed, "email": "a+b@example.com"}).
		AssertOk().
		AssertSee("password=[" + typed + "] token=[tok-1] fields=3")
}

// A _token the test writes into the form is the one the form carries, so a
// test can post somebody else's token to the guard that has to refuse it.
func TestATokenInTheFormReplacesTheOneOnThePage(t *testing.T) {
	client := test.NewClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(tokenPage))
			return
		}
		_ = r.ParseForm()
		_, _ = w.Write([]byte("form carries " + strings.Join(r.PostForm["_token"], ",")))
	}))

	client.Get("/invoices/1")
	client.Post("/invoices/1", map[string]string{"_token": "stolen"}).AssertSee("form carries stolen")

	client.Get("/invoices/1")
	client.Post("/invoices/1", nil).AssertSee("form carries tok-1")
}
