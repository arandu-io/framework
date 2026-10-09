package unit

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/observability/errorpage"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/exception"
	"github.com/arandu-io/hesape/http/exceptions"
	hvalidation "github.com/arandu-io/hesape/validation"
)

// domainError is an application's own failure, declaring its status the one
// way the adapter reads: a method, matched by its method set. It imports
// nothing from the framework to do it.
type domainError struct {
	status int
	cause  error
}

func (e domainError) Error() string   { return fmt.Sprintf("domain failure %d", e.status) }
func (e domainError) HTTPStatus() int { return e.status }
func (e domainError) Unwrap() error   { return e.cause }

// failing returns a router with one action that returns err.
func failing(err error) *fhttp.Router {
	r := fhttp.NewRouter().WithFlash(security.NewFlash(make([]byte, 32), false))
	r.Action(http.MethodPost, "/invoices/42", func(*fhttp.Context) error { return err })
	return r
}

// serve drives one request through the action, as htmx or as a whole page.
func serve(t *testing.T, err error, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/invoices/42", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	failing(err).ServeHTTP(rec, req)
	return rec
}

func TestAnErrorThatNamesItsStatusIsAnsweredWithIt(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"model not found", model.ErrModelNotFound, http.StatusNotFound},
		{"model not found, typed", &model.ModelNotFoundError{Model: "invoices", IDs: []any{42}}, http.StatusNotFound},
		{"record not found", database.ErrRecordNotFound, http.StatusNotFound},
		{"forbidden", security.ErrForbidden, http.StatusForbidden},
		{"csrf", security.ErrCSRF, 419},
		{"unique violation", database.ErrUniqueViolation, http.StatusConflict},
		{"declared status", domainError{status: http.StatusConflict}, http.StatusConflict},
		{"declared server status", domainError{status: http.StatusServiceUnavailable}, http.StatusServiceUnavailable},
		// The order is exception.StatusOf's: the status an error declares is
		// the explicit statement and wins over a sentinel it wraps, which may
		// only be its cause.
		{"declared status before the sentinel it wraps", domainError{status: http.StatusConflict, cause: security.ErrForbidden}, http.StatusConflict},
		{"HTTPError before the sentinel it wraps", &exception.HTTPError{Status: http.StatusNotFound, Err: security.ErrForbidden}, http.StatusNotFound},
	}

	for _, tc := range cases {
		for _, wrapped := range []bool{false, true} {
			err := tc.err
			name := tc.name
			if wrapped {
				err = fmt.Errorf("loading invoice 42 from billing_secret_table: %w", tc.err)
				name += ", wrapped"
			}

			t.Run(name+", whole page", func(t *testing.T) {
				rec := serve(t, err, false)
				if rec.Code != tc.want {
					t.Fatalf("status = %d, want %d", rec.Code, tc.want)
				}
				if got := rec.Header().Get("HX-Refresh"); got != "" {
					t.Errorf("a whole-page request was answered with HX-Refresh %q", got)
				}
				if body := rec.Body.String(); strings.Contains(body, "billing_secret_table") || strings.Contains(body, err.Error()) {
					t.Errorf("the error's own text reached the person: %q", body)
				}
				if strings.TrimSpace(rec.Body.String()) == "" {
					t.Error("the answer has no sentence for the person to read")
				}
				if got := rec.Header().Get("Content-Type"); got == exception.ProblemContentType {
					t.Error("a page was answered with a problem document")
				}
			})

			t.Run(name+", htmx", func(t *testing.T) {
				rec := serve(t, err, true)
				if rec.Code != tc.want {
					t.Fatalf("status = %d, want %d", rec.Code, tc.want)
				}
				// htmx swaps no 4xx, so the answer it acts on is the reload.
				if got := rec.Header().Get("HX-Refresh"); got != "true" {
					t.Errorf("HX-Refresh = %q, want true: without it the click does nothing visible", got)
				}
			})

			t.Run(name+", json", func(t *testing.T) {
				rec := serveJSON(t, failing(err))
				if rec.Code != tc.want {
					t.Fatalf("status = %d, want %d", rec.Code, tc.want)
				}
				p := problemOf(t, rec)
				if p.Status != tc.want {
					t.Errorf("the problem says status %d, want %d", p.Status, tc.want)
				}
				if p.Errors != nil {
					t.Errorf("a refusal carries an errors member: %v", p.Errors)
				}
				if body := rec.Body.String(); strings.Contains(body, "billing_secret_table") || strings.Contains(body, err.Error()) {
					t.Errorf("the error's own text reached the client: %q", body)
				}
				if got := rec.Header().Get("HX-Refresh"); got != "" {
					t.Errorf("a JSON client was answered with HX-Refresh %q", got)
				}
			})
		}
	}
}

// TestTheAdapterReadsTheOneStatusTable holds the adapter to exception.StatusOf:
// every error the table claims is answered with the status the table reads off
// it, in both representations. A table of the adapter's own, even one with the
// same rows in another order, fails here on the error that declares a status
// and wraps a sentinel.
//
// Validation failures are in the corpus for the JSON representation only. A
// page answers them with the redirect back, which is not a status the table
// has an opinion on.
func TestTheAdapterReadsTheOneStatusTable(t *testing.T) {
	rejected := validation.Errors{"title": {"this field is required"}}
	corpus := []struct {
		err        error
		validation bool
	}{
		{err: model.ErrModelNotFound},
		{err: &model.ModelNotFoundError{Model: "invoices", IDs: []any{42}}},
		{err: database.ErrRecordNotFound},
		{err: security.ErrForbidden},
		{err: security.ErrCSRF},
		{err: database.ErrUniqueViolation},
		{err: domainError{status: http.StatusConflict}},
		{err: domainError{status: http.StatusConflict, cause: security.ErrForbidden}},
		{err: domainError{status: http.StatusForbidden, cause: database.ErrRecordNotFound}},
		{err: &exception.HTTPError{Status: http.StatusNotFound, Err: security.ErrForbidden}},
		{err: &exception.HTTPError{Status: http.StatusTooManyRequests}},
		{err: rejected, validation: true},
		{err: hvalidation.WithMessages(rejected), validation: true},
	}

	for _, tc := range corpus {
		err := fmt.Errorf("handling: %w", tc.err)
		want, claimed := exception.StatusOf(err)
		if !claimed {
			t.Fatalf("%T: exception.StatusOf does not claim it, so it does not belong in this corpus", tc.err)
		}

		if rec := serveJSON(t, failing(err)); rec.Code != want {
			t.Errorf("%v, json: answered %d, exception.StatusOf reads %d", tc.err, rec.Code, want)
		}
		if tc.validation {
			continue
		}
		if rec := serve(t, err, false); rec.Code != want {
			t.Errorf("%v, page: answered %d, exception.StatusOf reads %d", tc.err, rec.Code, want)
		}
	}
}

// carrying is an application's own failure that declares its status and the
// headers its answer carries, the two by method set. It imports nothing from
// the framework to do it.
type carrying struct {
	status  int
	headers http.Header
	cause   error
}

func (e carrying) Error() string           { return fmt.Sprintf("carrying %d", e.status) }
func (e carrying) HTTPStatus() int         { return e.status }
func (e carrying) GetHeaders() http.Header { return e.headers }
func (e carrying) Unwrap() error           { return e.cause }

// A 429 tells the client to wait, and Retry-After tells it how long. The
// headers an error carries go out with its status in every representation,
// and they are the first carrier's in the chain, as the status is the first
// declarer's.
func TestTheHeadersAnErrorCarriesGoOutWithItsStatus(t *testing.T) {
	retry := http.Header{"Retry-After": {"30"}}
	carriers := map[string]error{
		"own type":          carrying{status: http.StatusTooManyRequests, headers: retry},
		"own type, wrapped": fmt.Errorf("exporting invoices: %w", carrying{status: http.StatusTooManyRequests, headers: retry}),
		"the throttle":      exceptions.NewThrottleRequestsException("", nil, retry, 0),
		"first in the chain wins": carrying{
			status:  http.StatusTooManyRequests,
			headers: retry,
			cause:   carrying{status: http.StatusServiceUnavailable, headers: http.Header{"Retry-After": {"600"}}},
		},
	}
	asks := map[string]func(error) *httptest.ResponseRecorder{
		"whole page": func(err error) *httptest.ResponseRecorder { return serve(t, err, false) },
		"htmx":       func(err error) *httptest.ResponseRecorder { return serve(t, err, true) },
		"json":       func(err error) *httptest.ResponseRecorder { return serveJSON(t, failing(err)) },
	}

	for name, err := range carriers {
		for how, ask := range asks {
			t.Run(name+", "+how, func(t *testing.T) {
				rec := ask(err)
				if rec.Code != http.StatusTooManyRequests {
					t.Fatalf("status = %d, want 429", rec.Code)
				}
				if got := rec.Header().Values("Retry-After"); len(got) != 1 || got[0] != "30" {
					t.Errorf("Retry-After = %q, want [30]: the client is told to wait and not for how long", got)
				}
			})
		}
	}

	if rec := serve(t, security.ErrForbidden, false); rec.Header().Get("Retry-After") != "" {
		t.Errorf("an error carrying no headers was answered with Retry-After %q", rec.Header().Get("Retry-After"))
	}
}

// Nothing claims an arbitrary error, and it keeps going to the panic path with
// the error itself, so the recover middleware classifies and logs it as before.
func TestAnUnclaimedErrorStillPanics(t *testing.T) {
	cause := errors.New("connection refused")

	defer func() {
		v := recover()
		err, _ := v.(error)
		if !errors.Is(err, cause) {
			t.Fatalf("panicked with %v, want the error the handler returned", v)
		}
	}()
	serve(t, fmt.Errorf("listing invoices: %w", cause), false)
	t.Fatal("an unclaimed error was answered without a panic")
}

func TestAnUnclaimedErrorIsStill500BehindRecover(t *testing.T) {
	h := middleware.Recover(false, errorpage.Options{})(failing(errors.New("connection refused")))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/invoices/42", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// A declared status outside 400-599 is a defect in the error type: 200 would
// say the request worked. It is refused loudly, with the error still in the
// chain.
func TestADeclaredStatusOutsideTheErrorRangePanics(t *testing.T) {
	for _, status := range []int{0, http.StatusOK, http.StatusFound, 399, 600} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			declared := domainError{status: status}
			defer func() {
				v := recover()
				err, _ := v.(error)
				if err == nil || !errors.As(err, new(domainError)) {
					t.Fatalf("panicked with %v, want an error carrying the declared one", v)
				}
				if !strings.Contains(err.Error(), fmt.Sprint(status)) {
					t.Errorf("the panic does not name the status: %v", err)
				}
			}()
			serve(t, declared, false)
			t.Fatalf("status %d was answered", status)
		})
	}
}

// The validation branch runs before the status table and is unchanged: the
// flash and the redirect back.
func TestValidationErrorsAreStillFlashedAndRedirected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/invoices/42", nil)
	req.Host = "example.test"
	req.Header.Set("Referer", "http://example.test/invoices/42/edit")

	rec := httptest.NewRecorder()
	failing(fmt.Errorf("saving: %w", validation.Errors{"title": {"this field is required"}})).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/invoices/42/edit" {
		t.Errorf("Location = %q, want the form it came from", got)
	}
}

// A failed Validate is the same rejection as validation.Errors: the exception
// reads as one, so a page gets the flash and the redirect back where it used to
// reach the panic path as an error nobody claimed.
func TestAFailedValidateIsFlashedAndRedirected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/invoices/42", nil)
	req.Host = "example.test"
	req.Header.Set("Referer", "http://example.test/invoices/42/edit")

	failed := hvalidation.WithMessages(map[string][]string{"title": {"this field is required"}})
	rec := httptest.NewRecorder()
	failing(fmt.Errorf("saving: %w", failed)).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/invoices/42/edit" {
		t.Errorf("Location = %q, want the form it came from", got)
	}
	if !setsCookie(rec, security.FlashCookieName) {
		t.Error("the messages were not flashed, so the form comes back with no reason given")
	}
}

// Without a flash wired, a rejection has nowhere to go and it still reaches
// the panic path rather than being answered as a bare status.
func TestValidationErrorsWithoutAFlashStillPanic(t *testing.T) {
	r := fhttp.NewRouter()
	r.Action(http.MethodPost, "/invoices", func(*fhttp.Context) error {
		return validation.Errors{"title": {"this field is required"}}
	})

	defer func() {
		if recover() == nil {
			t.Fatal("a rejection with no flash wired was answered without a panic")
		}
	}()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/invoices", nil))
}

// A body cut off by the size limit reaches the action as the error Bind
// returns, and that error declares 413 through HTTPStatus: the adapter answers
// it as the limit, not as a failure of the application.
func TestABodyOverTheLimitIsAnswered413ThroughTheAdapter(t *testing.T) {
	r := fhttp.NewRouter()
	r.Action(http.MethodPost, "/notes", func(ctx *fhttp.Context) error {
		var in struct {
			Body string `form:"body"`
		}
		return ctx.Bind(&in)
	})

	for _, contentType := range []string{"application/x-www-form-urlencoded", "application/json"} {
		body := "body=" + strings.Repeat("x", 64)
		if contentType == "application/json" {
			body = `{"body":"` + strings.Repeat("x", 64) + `"}`
		}
		req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		// A chunked body announces no length, so only the reader can stop it.
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		req.Body = http.MaxBytesReader(rec, req.Body, 16)

		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: status = %d, want 413", contentType, rec.Code)
		}
	}
}
