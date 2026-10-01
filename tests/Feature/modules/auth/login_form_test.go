package feature

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

type copiedLoginRequestKey struct{}

// TestSignInNeverParsesAMultipartBody posts a multipart body with a part too
// large for memory to the sign-in handler, through a copy of the request the
// way the pipeline hands it over. The handler reads its form without parsing
// the multipart body, so nothing is written to a temporary file that net/http
// would not remove.
func TestSignInNeverParsesAMultipartBody(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	if os.TempDir() != dir {
		t.Skipf("the temporary directory is not read from TMPDIR here (%s)", os.TempDir())
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	sessions := security.NewSessionStore(key, time.Hour, false, security.NewMemoryBackend())
	svc := auth.NewService(auth.NewUserRepo(nil), sessions, security.NewCSRF(key, time.Hour))
	router := fhttp.NewRouter()
	auth.New(svc, auth.FixedTenant("tenant-1")).Routes(router)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), copiedLoginRequestKey{}, true)))
	})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("upload", "large.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte{'x'}, 33<<20)); err != nil {
		t.Fatalf("writing the file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, middleware.SignInPath, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d for a form with no email", rec.Code, http.StatusUnprocessableEntity)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d temporary files left behind by a multipart sign-in", len(entries))
	}
}
