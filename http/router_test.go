package http_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
)

func TestMountSharesTheRESTRouteTree(t *testing.T) {
	router := fhttp.NewRouter()
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "rest")
	}).Name("health")
	router.Mount("/rpc.example.Service/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "rpc:%s", r.URL.Path)
	})).Name("rpc.example")

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{name: "REST", method: http.MethodGet, path: "/health", want: "rest"},
		{name: "mounted subtree", method: http.MethodPost, path: "/rpc.example.Service/Call", want: "rpc:/rpc.example.Service/Call"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if got := recorder.Body.String(); got != test.want {
				t.Fatalf("body = %q, want %q", got, test.want)
			}
		})
	}

	if got := len(router.Routes()); got != 2 {
		t.Fatalf("route count = %d, want 2", got)
	}
	path, err := router.Table().URL("rpc.example")
	if err != nil {
		t.Fatalf("mounted route URL: %v", err)
	}
	if path != "/rpc.example.Service/" {
		t.Fatalf("mounted route URL = %q", path)
	}
}
