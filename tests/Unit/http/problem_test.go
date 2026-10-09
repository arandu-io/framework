package unit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/exception"
	hvalidation "github.com/arandu-io/hesape/validation"
)

// serveJSON drives one request through r as a client that asked for JSON.
func serveJSON(t *testing.T, r *fhttp.Router) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/invoices/42", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// problemOf reads the answer as a problem document, and fails the test when it
// is not one.
func problemOf(t *testing.T, rec *httptest.ResponseRecorder) exception.Problem {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != exception.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %q; body %q", got, exception.ProblemContentType, rec.Body)
	}
	var p exception.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("the body is not a problem document: %v; body %q", err, rec.Body)
	}
	return p
}

// setsCookie reports whether the answer sets the named cookie.
func setsCookie(rec *httptest.ResponseRecorder, name string) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return true
		}
	}
	return false
}

// A client that wants JSON has no form to be sent back to, so a rejection is
// answered in the body: 422, a problem document, and the messages keyed by the
// field each belongs to. A failed Validate is the same rejection, and so is an
// XHR that is not htmx, which is how a script that never set Accept asks.
func TestAJSONClientGetsTheValidationProblem(t *testing.T) {
	messages := map[string][]string{
		"title": {"this field is required"},
		"total": {"must be at least 1", "must be a whole number"},
	}
	rejections := map[string]error{
		"validation.Errors":          validation.Errors(messages),
		"validation.Errors, wrapped": fmt.Errorf("saving invoice 42: %w", validation.Errors(messages)),
		"a failed Validate":          fmt.Errorf("saving invoice 42: %w", hvalidation.WithMessages(messages)),
	}
	asks := map[string]http.Header{
		"Accept":           {"Accept": {"application/json"}},
		"X-Requested-With": {"X-Requested-With": {"XMLHttpRequest"}},
	}

	for name, err := range rejections {
		for how, header := range asks {
			t.Run(name+", "+how, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/invoices/42", nil)
				req.Header = header.Clone()
				req.Header.Set("Referer", "http://example.com/invoices/42/edit")
				rec := httptest.NewRecorder()
				failing(err).ServeHTTP(rec, req)

				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want 422", rec.Code)
				}
				p := problemOf(t, rec)
				if p.Status != http.StatusUnprocessableEntity {
					t.Errorf("the problem says status %d, want 422", p.Status)
				}
				if !reflect.DeepEqual(p.Errors, messages) {
					t.Errorf("errors member = %v, want %v", p.Errors, messages)
				}
				if got := rec.Header().Get("Location"); got != "" {
					t.Errorf("a JSON client was sent back to %q", got)
				}
				if setsCookie(rec, security.FlashCookieName) {
					t.Error("a JSON client was answered with a flash cookie it will never bring to a page")
				}
				if strings.Contains(rec.Body.String(), "saving invoice 42") {
					t.Errorf("the error's own text reached the client: %q", rec.Body)
				}
			})
		}
	}
}

// The flash is where a page's messages go, and a JSON client's go in the
// body, so a router with no flash wired still answers it rather than reaching
// the panic path a page reaches.
func TestAJSONClientIsAnsweredWithoutAFlash(t *testing.T) {
	r := fhttp.NewRouter()
	r.Action(http.MethodPost, "/invoices/42", func(*fhttp.Context) error {
		return validation.Errors{"title": {"this field is required"}}
	})

	rec := serveJSON(t, r)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if p := problemOf(t, rec); len(p.Errors["title"]) != 1 {
		t.Errorf("errors member = %v, want the message for title", p.Errors)
	}
}

// An empty rejection is a defect in the handler whichever representation was
// asked for: a 422 with nothing to correct is as useless to a client as a blank
// form is to a person.
func TestAnEmptyRejectionPanicsForAJSONClientToo(t *testing.T) {
	defer func() {
		v := recover()
		msg, _ := v.(string)
		if !strings.Contains(msg, "empty validation.Errors") {
			t.Fatalf("panicked with %v, want the empty-rejection message", v)
		}
	}()
	serveJSON(t, failing(validation.Errors{}))
	t.Fatal("an empty rejection was answered")
}

// htmx sends X-Requested-With and swaps HTML, so it is answered as a page even
// when it says it accepts JSON: a problem document swapped into a div is
// neither a page nor an answer.
func TestHTMXIsAnsweredAsAPageWhateverItAccepts(t *testing.T) {
	ask := func(err error) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/invoices/42", nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		failing(err).ServeHTTP(rec, req)
		return rec
	}

	rejected := ask(validation.Errors{"title": {"this field is required"}})
	if rejected.Header().Get("HX-Redirect") == "" {
		t.Errorf("a rejection to htmx was not sent back with HX-Redirect; status %d", rejected.Code)
	}

	refused := ask(security.ErrForbidden)
	if refused.Code != http.StatusForbidden || refused.Header().Get("HX-Refresh") != "true" {
		t.Errorf("a refusal to htmx = %d with HX-Refresh %q, want 403 and true", refused.Code, refused.Header().Get("HX-Refresh"))
	}
	for _, rec := range []*httptest.ResponseRecorder{rejected, refused} {
		if rec.Header().Get("Content-Type") == exception.ProblemContentType {
			t.Error("htmx was answered with a problem document")
		}
	}
}
