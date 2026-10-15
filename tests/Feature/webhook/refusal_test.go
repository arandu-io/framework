package feature

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// TestAnEmptyRefusalIsWhatWriteHeaderAlonePuts holds RefusalEmpty, answered
// through the Framework adapter, to the answer of a handler that does nothing
// but call WriteHeader on the same router: the status, every header, and no
// body, byte for byte.
func TestAnEmptyRefusalIsWhatWriteHeaderAlonePuts(t *testing.T) {
	t.Parallel()

	processor := &filing{}
	m := mount(t, hwebhook.Config{Receivers: []hwebhook.Receiver{
		{
			Name: "cielo", Path: "cielo/{token}", Resolve: cieloResolver,
			Validator: hwebhook.HeaderSecret{Header: "X-Perata-Notification"}, MaxBodyBytes: 64 << 10,
			ProcessSync: processor, RefusalBody: hwebhook.RefusalEmpty,
			Statuses: hwebhook.Statuses{InvalidSignature: http.StatusNotFound, EmptyBody: http.StatusBadRequest},
		},
		{Name: "texted", Path: "texted/{token}", Resolve: cieloResolver, Validator: hwebhook.HeaderSecret{Header: "X-Perata-Notification"}, ProcessSync: processor},
	}})
	m.router.Action(http.MethodPost, "/reference/{status}", func(ctx *fhttp.Context) error {
		status, _ := strconv.Atoi(ctx.Param("status"))
		ctx.Response.WriteHeader(status)
		return nil
	})
	reference := func(status int) *httptest.ResponseRecorder {
		return serve(t, m.router, httptest.NewRequest(http.MethodPost, "/reference/"+strconv.Itoa(status), nil))
	}
	path := "/webhooks/cielo/" + token("acme", "ep-1")
	for name, refusal := range map[string]struct {
		req    *http.Request
		status int
	}{
		"a tampered token":        {notification("/webhooks/cielo/"+token("acme", "ep-1")+"0", `{"a":1}`, cieloSecret), http.StatusNotFound},
		"the wrong secret":        {notification(path, `{"a":1}`, "not-the-secret"), http.StatusNotFound},
		"an empty body":           {notification(path, "", cieloSecret), http.StatusBadRequest},
		"a body above the limit":  {notification(path, strings.Repeat("x", 64<<10+1), cieloSecret), http.StatusRequestEntityTooLarge},
		"a resolver that is down": {notification("/webhooks/cielo/down.x.y", `{"a":1}`, cieloSecret), http.StatusServiceUnavailable},
		"a refusal wanting JSON": {func() *http.Request {
			r := notification(path, "", cieloSecret)
			r.Header.Set("Accept", "application/json")
			return r
		}(), http.StatusBadRequest},
	} {
		got, want := serve(t, m.router, refusal.req), reference(refusal.status)
		if got.Code != refusal.status || got.Code != want.Code {
			t.Errorf("%s answered %d, want %d", name, got.Code, refusal.status)
		}
		if !reflect.DeepEqual(got.Header(), want.Header()) {
			t.Errorf("%s answered the headers %v, and WriteHeader alone puts %v", name, got.Header(), want.Header())
		}
		if got.Body.Len() != 0 || want.Body.Len() != 0 {
			t.Errorf("%s answered the body %q", name, got.Body.String())
		}
	}

	// The default keeps answering through the adapter, with its sentence.
	if rec := serve(t, m.router, notification("/webhooks/texted/"+token("acme", "ep-1"), `{"a":1}`, "not-the-secret")); rec.Code != http.StatusForbidden || rec.Body.Len() == 0 {
		t.Fatalf("a refusal of a receiver with RefusalText answered %d %q", rec.Code, rec.Body.String())
	}
	if processor.calls != 0 {
		t.Fatalf("a refused call reached the processor %d times", processor.calls)
	}
}
