package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/validation"
)

var storePost = validation.MustCompile(validation.Rules{
	"title": "required|max:255",
	"body":  "required",
})

func TestValidateReturnsANilErrorWhenNothingFailed(t *testing.T) {
	// A typed nil here would make `if err != nil` true on every successful
	// request in the framework, and every accepted form would be answered with a
	// redirect back to itself carrying no messages.
	var (
		ran   bool
		title string
	)
	r := httpx.NewRouter()
	r.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		in, err := ctx.Validate(storePost)
		if err != nil {
			t.Errorf("Validate returned %v on input that passes every rule", err)
			return nil
		}
		ran, title = true, in.String("title")
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader("title=A+post&body=Some+words"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if !ran {
		t.Fatal("the handler never got past Validate")
	}
	if title != "A post" {
		t.Errorf("in.String(title) = %q", title)
	}
}

func TestValidateReturnsTheMessagesForTheHandlerToReturn(t *testing.T) {
	var got validation.Errors
	r := httpx.NewRouter()
	r.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		_, err := ctx.Validate(storePost)
		if err == nil {
			t.Fatal("an empty title passed `required`")
		}
		// The error IS the answer: the handler returns it and writes nothing.
		var rejected validation.Errors
		if !errorsAs(err, &rejected) {
			t.Fatalf("Validate returned %T, want validation.Errors", err)
		}
		got = rejected
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader("title=&body="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if len(got["title"]) == 0 || len(got["body"]) == 0 {
		t.Errorf("errors = %v, want a message on each empty field", got)
	}
}

func TestValidateWithNoRuleSetIsAWiringMistakeAndSaysSo(t *testing.T) {
	// A nil set would pass every field and hand the handler an empty Input,
	// which writes zero values into a repository with nothing to show for it.
	r := httpx.NewRouter()
	r.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		_, err := ctx.Validate(nil)
		return err
	})

	defer func() {
		got, ok := recover().(string)
		if !ok || !strings.Contains(got, "no rule set") {
			t.Errorf("panicked with %v, want a message naming the fix", got)
		}
	}()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/posts", nil))
}

// errorsAs is errors.As, named here so the test above reads as the branch a
// service writes when it translates a driver's conflict onto a field.
func errorsAs(err error, target *validation.Errors) bool {
	e, ok := err.(validation.Errors)
	if ok {
		*target = e
	}
	return ok
}
