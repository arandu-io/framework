package feature

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
)

// spillSize is larger than the memory a multipart form is parsed into, so the
// file part has to be written to a temporary file.
const spillSize = 33 << 20

type copiedRequestKey struct{}

// multipartBody is a form with an optional _token field and one file part of
// spillSize bytes.
func multipartBody(t *testing.T, token string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if token != "" {
		if err := mw.WriteField("_token", token); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	fw, err := mw.CreateFormFile("upload", "large.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte{'x'}, spillSize)); err != nil {
		t.Fatalf("writing the file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return &body, mw.FormDataContentType()
}

// uploadPipeline is CSRFProtect behind a layer that copies the request with
// WithContext, the way the observability middleware does, which is the copy
// net/http never cleans up after.
func uploadPipeline(csrf *security.CSRF, handler http.Handler) http.Handler {
	h := fhttp.Chain(handler, middleware.CSRFProtect(csrf, func(*http.Request) string { return "session-1" }))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), copiedRequestKey{}, true)))
	})
}

// isolatedTempDir points the temporary directory at a fresh one, so what the
// parse leaves behind can be counted.
func isolatedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	if os.TempDir() != dir {
		t.Skipf("the temporary directory is not read from TMPDIR here (%s)", os.TempDir())
	}
	return dir
}

func leftovers(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	return len(entries)
}

// TestCSRFRefusedUploadLeavesNoTemporaryFiles is the unauthenticated upload: a
// multipart body with a part too large for memory and no valid token. It is
// refused, and nothing it caused to be written stays on disk.
func TestCSRFRefusedUploadLeavesNoTemporaryFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no token", ""},
		{"invalid token", "not-a-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedTempDir(t)
			csrf := security.NewCSRF(appKey, time.Hour)
			h := uploadPipeline(csrf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("the handler was reached without a valid token")
			}))

			body, contentType := multipartBody(t, tc.token)
			r := httptest.NewRequest(http.MethodPost, "/uploads", body)
			r.Header.Set("Content-Type", contentType)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != middleware.StatusCSRFExpired {
				t.Fatalf("status = %d, want %d", rec.Code, middleware.StatusCSRFExpired)
			}
			if n := leftovers(t, dir); n != 0 {
				t.Fatalf("%d temporary files left behind by a refused upload", n)
			}
		})
	}
}

// TestCSRFAcceptedUploadIsReadableAndRemoved is the native form submission: the
// token in the body. The handler reads the file through the request it was
// handed, and the temporary file is removed once it has returned.
func TestCSRFAcceptedUploadIsReadableAndRemoved(t *testing.T) {
	dir := isolatedTempDir(t)
	csrf := security.NewCSRF(appKey, time.Hour)
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var read int64
	var spilled int
	h := uploadPipeline(csrf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("upload")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			return
		}
		defer func() { _ = f.Close() }()
		read, _ = io.Copy(io.Discard, f)
		spilled = leftovers(t, dir)
	}))

	body, contentType := multipartBody(t, token)
	r := httptest.NewRequest(http.MethodPost, "/uploads", body)
	r.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid form token", rec.Code)
	}
	if read != spillSize {
		t.Fatalf("the handler read %d bytes of the upload, want %d", read, spillSize)
	}
	if spilled == 0 {
		t.Fatal("the upload was never written to a temporary file, so this test proves nothing about removing one")
	}
	if n := leftovers(t, dir); n != 0 {
		t.Fatalf("%d temporary files left behind after the handler returned", n)
	}
}

// TestCSRFHeaderTokenLeavesTheBodyUnread covers the htmx upload: the token is in
// the header, so the body reaches the handler unparsed and can be streamed.
func TestCSRFHeaderTokenLeavesTheBodyUnread(t *testing.T) {
	csrf := security.NewCSRF(appKey, time.Hour)
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	h := uploadPipeline(csrf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.MultipartReader(); err != nil {
			t.Errorf("MultipartReader: %v: the body was parsed before the handler", err)
		}
	}))

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("note", "hello")
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/uploads", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-CSRF-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid header token", rec.Code)
	}
}

// TestCSRFIgnoresTheTokenInANonFormBody covers a body that is not a form: a
// _token in it is not read, and the request is refused as carrying none.
func TestCSRFIgnoresTheTokenInANonFormBody(t *testing.T) {
	csrf := security.NewCSRF(appKey, time.Hour)
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	h := uploadPipeline(csrf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the handler was reached with the token in a JSON body")
	}))

	r := httptest.NewRequest(http.MethodPost, "/uploads", bytes.NewBufferString(`{"_token":"`+token+`"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d", rec.Code, middleware.StatusCSRFExpired)
	}
}
