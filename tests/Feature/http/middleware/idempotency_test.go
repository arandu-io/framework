package feature

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/cache"
)

// Every store that can hold a lock is an IdempotencyStore as it stands; these
// are the two in hesape/cache, in one process and in a table.
var (
	_ middleware.IdempotencyStore = (*cache.ArrayStore)(nil)
	_ middleware.IdempotencyStore = (*cache.DatabaseStore)(nil)
)

// charges is a write endpoint behind RequireToken and Idempotent, on a real
// router. Its handler counts how many times it ran and answers 201 with the
// count, a Location, a cookie and the body it was sent.
type charges struct {
	h      http.Handler
	runs   atomic.Int32
	store  middleware.IdempotencyStore
	status atomic.Int32 // the status the next run answers with; 201 when zero
	gate   chan struct{}
	inside chan struct{}
}

// Two tokens for one account in one tenant would be one subject; these are two
// accounts, and a third with the first one's id in another tenant.
var (
	alice      = security.Subject{ID: "u-1", Tenant: "acme"}
	bob        = security.Subject{ID: "u-2", Tenant: "acme"}
	aliceElsew = security.Subject{ID: "u-1", Tenant: "globex"}
)

func newCharges(store middleware.IdempotencyStore) *charges {
	c := &charges{store: store}
	tokens := newIssuedTokens()
	tokens.issue("tok-alice", alice)
	tokens.issue("tok-bob", bob)
	tokens.issue("tok-alice-globex", aliceElsew)

	r := fhttp.NewRouter()
	write := func(w http.ResponseWriter, req *http.Request) {
		n := c.runs.Add(1)
		if c.inside != nil {
			c.inside <- struct{}{}
			<-c.gate
		}
		body, _ := io.ReadAll(req.Body)
		status := int(c.status.Load())
		if status == 0 {
			status = http.StatusCreated
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", fmt.Sprintf("/charges/%d", n))
		http.SetCookie(w, &http.Cookie{Name: "seen", Value: "1"})
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"run":%d,"got":%q}`, n, body)
	}
	guards := []fhttp.Middleware{middleware.RequireToken(tokens), middleware.Idempotent(store, time.Hour)}
	r.Post("/charges", write, guards...)
	r.Put("/charges/{id}", write, guards...)
	r.Get("/charges", write, guards...)
	c.h = r
	return c
}

// send makes one request as the holder of token, with the key when one is
// given, and returns the answer.
func (c *charges) send(method, path, token, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	return rec
}

func TestARetryIsAnsweredWithTheFirstAnswerAndDoesNotRunAgain(t *testing.T) {
	c := newCharges(cache.NewArrayStore())

	first := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)
	retry := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)

	if n := c.runs.Load(); n != 1 {
		t.Fatalf("the handler ran %d times for one key, want once", n)
	}
	if first.Code != http.StatusCreated || retry.Code != first.Code {
		t.Errorf("status: first %d, retry %d, want 201 both", first.Code, retry.Code)
	}
	if retry.Body.String() != first.Body.String() {
		t.Errorf("the retry answered %q, want the first answer %q", retry.Body.String(), first.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"got":"{\"amount\":100}"`) {
		t.Errorf("the handler was not handed the body it was sent: %s", first.Body.String())
	}
	for _, h := range []string{"Location", "Content-Type"} {
		if retry.Header().Get(h) != first.Header().Get(h) {
			t.Errorf("%s: retry %q, first %q", h, retry.Header().Get(h), first.Header().Get(h))
		}
	}
	if got := retry.Header().Get("Idempotent-Replayed"); got != "true" {
		t.Errorf("Idempotent-Replayed on the retry = %q, want true", got)
	}
	if got := first.Header().Get("Idempotent-Replayed"); got != "" {
		t.Errorf("Idempotent-Replayed on the answer that ran = %q, want none", got)
	}
	if first.Header().Get("Set-Cookie") == "" {
		t.Fatal("the handler's cookie did not reach the first answer; the test proves nothing about the replay")
	}
	if got := retry.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("the replay set a cookie %q; a cookie is not on the list of headers kept", got)
	}
}

func TestTheSameKeyForAnotherRequestIsRefused(t *testing.T) {
	cases := map[string]struct{ method, path, body string }{
		"another body":    {http.MethodPost, "/charges", `{"amount":999}`},
		"another address": {http.MethodPost, "/charges?currency=eur", `{"amount":100}`},
		"another method":  {http.MethodPut, "/charges/1", `{"amount":100}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newCharges(cache.NewArrayStore())
			c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)

			rec := c.send(tc.method, tc.path, "tok-alice", "key-1", tc.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status %d, want 422 for a key reused on %s", rec.Code, name)
			}
			if n := c.runs.Load(); n != 1 {
				t.Errorf("the handler ran %d times, want once: a reused key must not run the second request", n)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want a problem document for a JSON client", ct)
			}
		})
	}
}

func TestConcurrentDuplicatesDoNotBothRun(t *testing.T) {
	c := newCharges(cache.NewArrayStore())
	c.inside = make(chan struct{})
	c.gate = make(chan struct{})

	var first *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		first = c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)
	}()
	<-c.inside // the first copy is now running, holding the key

	duplicate := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)

	close(c.gate)
	wg.Wait()
	c.inside = nil

	if duplicate.Code != http.StatusConflict {
		t.Errorf("a duplicate sent while the first was running answered %d, want 409", duplicate.Code)
	}
	if duplicate.Header().Get("Retry-After") == "" {
		t.Error("the 409 carries no Retry-After")
	}
	if n := c.runs.Load(); n != 1 {
		t.Fatalf("the handler ran %d times for two concurrent copies of one key, want once", n)
	}

	after := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)
	if after.Body.String() != first.Body.String() || after.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("a retry after the first finished answered %d %q, want the replay of %q", after.Code, after.Body.String(), first.Body.String())
	}
	if n := c.runs.Load(); n != 1 {
		t.Errorf("the handler ran %d times, want once", n)
	}
}

// The key is the subject's: the same string sent by two accounts, or by one
// account id in two tenants, is two keys.
func TestTheSameKeyFromAnotherSubjectIsAnotherKey(t *testing.T) {
	c := newCharges(cache.NewArrayStore())

	a := c.send(http.MethodPost, "/charges", "tok-alice", "shared-key", `{"amount":100}`)
	b := c.send(http.MethodPost, "/charges", "tok-bob", "shared-key", `{"amount":100}`)
	g := c.send(http.MethodPost, "/charges", "tok-alice-globex", "shared-key", `{"amount":100}`)

	if n := c.runs.Load(); n != 3 {
		t.Fatalf("the handler ran %d times for three subjects, want three", n)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"another account": b, "another tenant": g} {
		if rec.Header().Get("Idempotent-Replayed") != "" || rec.Body.String() == a.Body.String() {
			t.Errorf("%s was answered with the first account's answer %q", name, rec.Body.String())
		}
	}
}

// A refusal and a failure are not kept, so a retry runs: the refusal changed
// nothing, and the client can fix the request and send it under the same key.
func TestARefusalOrAFailureIsNotKept(t *testing.T) {
	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newCharges(cache.NewArrayStore())
			c.status.Store(int32(status))
			if rec := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":-1}`); rec.Code != status {
				t.Fatalf("the first answer was %d, want %d", rec.Code, status)
			}

			c.status.Store(0)
			retry := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)

			if retry.Code != http.StatusCreated || c.runs.Load() != 2 {
				t.Errorf("after a %d the retry answered %d with %d runs, want 201 and a second run", status, retry.Code, c.runs.Load())
			}
		})
	}
}

func TestReadsAndRequestsWithoutAKeyAreNotTouched(t *testing.T) {
	c := newCharges(cache.NewArrayStore())

	c.send(http.MethodGet, "/charges", "tok-alice", "key-1", "")
	c.send(http.MethodGet, "/charges", "tok-alice", "key-1", "")
	if n := c.runs.Load(); n != 2 {
		t.Errorf("two GETs with one key ran %d times, want two: a read has nothing to replay", n)
	}

	c.send(http.MethodPost, "/charges", "tok-alice", "", `{"amount":100}`)
	c.send(http.MethodPost, "/charges", "tok-alice", "", `{"amount":100}`)
	if n := c.runs.Load(); n != 4 {
		t.Errorf("two POSTs with no key ran %d times in all, want four", n)
	}
}

func TestAMalformedKeyIsRefused(t *testing.T) {
	c := newCharges(cache.NewArrayStore())

	for name, keys := range map[string][]string{
		"too long":        {strings.Repeat("k", 256)},
		"control byte":    {"key\x01one"},
		"not ascii":       {"clé"},
		"two of them":     {"key-1", "key-2"},
		"an empty header": {""},
	} {
		r := httptest.NewRequest(http.MethodPost, "/charges", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer tok-alice")
		for _, k := range keys {
			r.Header.Add("Idempotency-Key", k)
		}
		rec := httptest.NewRecorder()
		c.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("the handler ran %d times for malformed keys, want none", n)
	}

	r := httptest.NewRequest(http.MethodPost, "/charges", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer tok-alice")
	r.Header.Set("Idempotency-Key", strings.Repeat("k", 255))
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Errorf("a 255-byte key answered %d, want 201", rec.Code)
	}
}

// Without a subject there is nobody to scope the key to, and a key kept
// unscoped is one caller's answer replayed to another. It is a wiring defect,
// and it panics rather than running the write unprotected.
func TestAKeyWithNoSubjectIsAWiringDefect(t *testing.T) {
	ran := false
	h := middleware.Idempotent(cache.NewArrayStore(), time.Hour)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		ran = true
	}))
	r := httptest.NewRequest(http.MethodPost, "/charges", strings.NewReader("{}"))
	r.Header.Set("Idempotency-Key", "key-1")

	recovered := func() (v any) {
		defer func() { v = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), r)
		return nil
	}()

	if recovered == nil {
		t.Fatal("Idempotent ran a keyed write with no subject on the request")
	}
	if ran {
		t.Error("the handler ran before the panic")
	}
	if !strings.Contains(fmt.Sprint(recovered), "RequireToken") {
		t.Errorf("the panic does not name the fix: %v", recovered)
	}
}

// flakyStore is an IdempotencyStore that fails or forgets on request.
type flakyStore struct {
	*cache.ArrayStore
	getErr    error
	missNext  atomic.Bool
	getsAsked atomic.Int32
}

func (s *flakyStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.getsAsked.Add(1)
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.missNext.CompareAndSwap(true, false) {
		return nil, cache.ErrNotFound
	}
	return s.ArrayStore.Get(ctx, key)
}

// The race the second read exists for: the first copy finishes and releases
// the key between the duplicate's read and the duplicate taking the lock.
func TestTheAnswerIsReadAgainUnderTheLock(t *testing.T) {
	store := &flakyStore{ArrayStore: cache.NewArrayStore()}
	c := newCharges(store)
	first := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)

	// The duplicate's first read sees nothing, as it would had it read just
	// before the first copy stored its answer.
	store.missNext.Store(true)
	dup := c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)

	if n := c.runs.Load(); n != 1 {
		t.Fatalf("the handler ran %d times: the duplicate took the free lock and ran without reading again", n)
	}
	if dup.Body.String() != first.Body.String() {
		t.Errorf("the duplicate answered %q, want the replay of %q", dup.Body.String(), first.Body.String())
	}
}

func TestAStoreThatCannotAnswerDoesNotRunTheWrite(t *testing.T) {
	outage := errors.New("store unreachable")
	c := newCharges(&flakyStore{ArrayStore: cache.NewArrayStore(), getErr: outage})

	recovered := func() (v any) {
		defer func() { v = recover() }()
		c.send(http.MethodPost, "/charges", "tok-alice", "key-1", `{"amount":100}`)
		return nil
	}()

	if n := c.runs.Load(); n != 0 {
		t.Errorf("the handler ran %d times without knowing whether it had already run", n)
	}
	err, ok := recovered.(error)
	if !ok || !errors.Is(err, outage) {
		t.Errorf("recovered %v, want the store's error on the panic path", recovered)
	}
}

// A handler that panics leaves the key free, so the retry runs rather than
// being answered 409 until the lock expires.
func TestAPanickingHandlerReleasesTheKey(t *testing.T) {
	store := cache.NewArrayStore()
	tokens := newIssuedTokens()
	tokens.issue("tok-alice", alice)
	var runs atomic.Int32
	h := fhttp.Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if runs.Add(1) == 1 {
			panic("boom")
		}
		w.WriteHeader(http.StatusCreated)
	}), middleware.RequireToken(tokens), middleware.Idempotent(store, time.Hour))

	send := func() (rec *httptest.ResponseRecorder, panicked bool) {
		defer func() { panicked = recover() != nil }()
		r := httptest.NewRequest(http.MethodPost, "/charges", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer tok-alice")
		r.Header.Set("Idempotency-Key", "key-1")
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec, false
	}

	if _, panicked := send(); !panicked {
		t.Fatal("the first run did not panic; the test proves nothing")
	}
	rec, _ := send()
	if rec.Code != http.StatusCreated || runs.Load() != 2 {
		t.Errorf("the retry after a panic answered %d with %d runs, want 201 and a second run", rec.Code, runs.Load())
	}
}

func TestIdempotentRefusesAMisconfigurationAtWiring(t *testing.T) {
	for name, wire := range map[string]func(){
		"nil store":    func() { middleware.Idempotent(nil, time.Hour) },
		"zero ttl":     func() { middleware.Idempotent(cache.NewArrayStore(), 0) },
		"negative ttl": func() { middleware.Idempotent(cache.NewArrayStore(), -time.Second) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Idempotent wired without complaint", name)
				}
			}()
			wire()
		}()
	}
}

// The body is read in full to be compared, and a limit set further out still
// holds: a body over it is answered 413 and the handler does not run.
func TestABodyOverTheLimitIsRefusedBeforeItRuns(t *testing.T) {
	c := newCharges(cache.NewArrayStore())
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8)
		c.h.ServeHTTP(w, r)
	})

	r := httptest.NewRequest(http.MethodPost, "/charges", strings.NewReader(`{"amount":100}`))
	r.Header.Set("Authorization", "Bearer tok-alice")
	r.Header.Set("Idempotency-Key", "key-1")
	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, r)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", rec.Code)
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("the handler ran %d times on a body over the limit", n)
	}
}
