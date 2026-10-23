package test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Status and Handler are what a kit outside this package needs to decide an
// answer itself: the code as it came back, and the handler the server serves.
func TestStatusAndHandlerHandBackWhatTheServerAnswers(t *testing.T) {
	teapot := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	s := &Server{t: t, handler: teapot}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("Handler answered %d, want the server's handler (%d)", rec.Code, http.StatusTeapot)
	}
	if got := (&Response{t: t, rec: rec}).Status(); got != http.StatusTeapot {
		t.Errorf("Status = %d, want %d", got, http.StatusTeapot)
	}
}
