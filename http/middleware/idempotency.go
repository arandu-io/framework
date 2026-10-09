package middleware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/cache"
)

// The two headers Idempotent speaks.
const (
	idempotencyKeyHeader = "Idempotency-Key"
	replayedHeader       = "Idempotent-Replayed"
)

// maxIdempotencyKey is the longest key accepted, in bytes. A key is an
// identifier the client generates -- a UUID is 36 characters -- and a header
// that is longer than this is not one.
const maxIdempotencyKey = 255

// idempotencyLockTTL is how long a request holding a key keeps other copies of
// itself out.
//
// It is twice the server's deadline on a whole response, so a duplicate cannot
// slip in while the first copy is still inside the time it is allowed to take;
// and it is short, because a process killed halfway through a request never
// releases the lock, and every retry until it expires is answered 409.
const idempotencyLockTTL = 2 * time.Minute

// replayedHeaders are the response headers kept with an answer and written
// again when it is replayed. The list is closed.
//
// What is left out is left out on purpose. Set-Cookie is the one that matters:
// replaying it would hand a second copy of a session, or of whatever else the
// first answer set, to every retry. X-Request-ID belongs to the request that is
// answering, not to the one that ran. Date, Content-Length and the rest are
// written by the server for the answer actually sent.
var replayedHeaders = []string{
	"Cache-Control",
	"Content-Encoding",
	"Content-Language",
	"Content-Location",
	"Content-Type",
	"Etag",
	"Last-Modified",
	"Location",
}

// IdempotencyStore is where Idempotent keeps the answers it replays, and the
// lock that keeps two copies of one request from running at once.
//
// It is the part of a hesape cache store Idempotent uses, under the same method
// names, so a store that can hold a lock satisfies it as it stands:
// *cache.ArrayStore inside one process, the database store or the RESP store
// across several. Across several processes it has to be a shared one -- a key
// kept in one process's memory is replayed by that process and run a second
// time by the next.
type IdempotencyStore interface {
	// Get returns the bytes stored under key, or cache.ErrNotFound when there
	// are none or they expired. Any other error is a store that could not
	// answer, and the request is not run.
	Get(ctx context.Context, key string) ([]byte, error)

	// Put stores value under key for ttl, replacing whatever was there.
	Put(ctx context.Context, key string, value []byte, ttl time.Duration) error

	cache.Locking
}

// Idempotent makes a write that carries an Idempotency-Key header run once,
// and answers every retry of it with the answer the first copy got.
//
// The first request with a key runs. Its status, the headers in a closed list
// -- Content-Type, Location, ETag and the other representation headers, never
// Set-Cookie -- and its body are stored for ttl. A retry with the same key and
// the same request is answered with that, byte for byte, carrying
// Idempotent-Replayed: true, and the handler does not run again. The same key
// with a different request -- another body, another method, another address --
// is answered 422: it is a client reusing a key, and replaying the first answer
// to a second question would be wrong whichever way it went.
//
// A retry that arrives while the first copy is still running is answered 409
// with Retry-After, and does not run: two copies of a payment in flight at once
// is the failure this exists to remove, and it is the one a check-then-run
// would let through. The lock is taken in the store, so it holds across
// processes when the store is shared.
//
// Only an answer below 400 is kept. A refusal changed nothing, by the meaning
// of the status, so its retry may run -- which is what lets a client fix the
// request and send it again under the same key. A server error is not kept
// either, so the client can retry it; making the handler safe to run again
// after one is the handler's transaction, not something a replay can supply.
// A request whose handler took the connection over keeps nothing either: what
// it sent went past the writer, so there is no answer to replay.
//
// GET, HEAD, OPTIONS and TRACE pass through untouched, and so does a request
// with no key. A key is one header value of 1 to 255 visible ASCII characters,
// spaces allowed; anything else is answered 400.
//
// # The key belongs to the subject
//
// The answer is stored under the tenant and the id of the subject on the
// request context, and the key the client sent, so two clients that happen to
// pick the same key never see each other's answers. The tenant comes from the
// subject and from nothing on the request. That makes the order of the
// pipeline part of the contract: Idempotent is mounted after the guard that
// carries the subject -- RequireToken, RequireAuth -- and a request that
// reaches it with a key and no subject panics, naming the fix, rather than
// keeping one caller's answer where another could replay it.
//
// The body is read in full before the handler runs, to compare it with the
// stored one, and handed to the handler unchanged. A body over the limit a
// middleware mounted earlier set is answered 413.
//
// A store that cannot answer before the handler runs is a panic, answered 500:
// running the write anyway is exactly what the caller asked not to happen. A
// store that cannot keep the answer after the handler ran is logged, because
// the answer has already been sent -- and a retry of that request will run
// again.
//
// ttl is how long a key is remembered, and it must be positive; a day is a
// usual choice, long enough to outlast any client's retries.
func Idempotent(store IdempotencyStore, ttl time.Duration) func(http.Handler) http.Handler {
	if store == nil {
		panic("middleware: Idempotent was given a nil IdempotencyStore. Pass a cache store that can hold a lock, shared between the processes that serve the route")
	}
	if ttl <= 0 {
		panic(fmt.Sprintf("middleware: Idempotent was given a ttl of %s. A key that is never remembered replays nothing, so the ttl must be positive", ttl))
	}
	m := idempotency{store: store, ttl: ttl}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.serve(next, w, r)
		})
	}
}

// idempotency is one mounted Idempotent.
type idempotency struct {
	store IdempotencyStore
	ttl   time.Duration
}

// storedResponse is what is kept under a key: the fingerprint of the request
// that ran, and the answer it got.
type storedResponse struct {
	Fingerprint []byte              `json:"fingerprint"`
	Status      int                 `json:"status"`
	Header      map[string][]string `json:"header,omitempty"`
	Body        []byte              `json:"body,omitempty"`
}

func (m idempotency) serve(next http.Handler, w http.ResponseWriter, r *http.Request) {
	if safeMethod(r.Method) {
		next.ServeHTTP(w, r)
		return
	}
	keys := r.Header.Values(idempotencyKeyHeader)
	if len(keys) == 0 {
		next.ServeHTTP(w, r)
		return
	}
	if len(keys) > 1 || !validIdempotencyKey(keys[0]) {
		refuse(w, r, http.StatusBadRequest, "the Idempotency-Key header must be one value of 1 to 255 visible ASCII characters")
		return
	}

	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.ID == "" || !auth.ValidTenant(subject.Tenant) {
		panic("middleware: Idempotent reached a request carrying an Idempotency-Key and no authenticated subject with an id and a tenant. " +
			"Mount it after RequireToken, RequireAuth or another guard that carries the subject: a key with nobody to scope it to would replay one caller's answer to another")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			refuse(w, r, http.StatusRequestEntityTooLarge, "the request body is too large")
			return
		}
		refuse(w, r, http.StatusBadRequest, "the request body could not be read")
		return
	}
	// A copy of the request, so the body put back is this handler's and the
	// request a middleware further out holds is left as it was.
	r = r.WithContext(r.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))

	ctx := r.Context()
	entry, lock := idempotencyKeys(subject, keys[0])
	fp := fingerprint(r, body)

	if stored, found := m.load(ctx, entry); found {
		replay(w, r, stored, fp)
		return
	}

	token := rand.Text()
	held, err := m.store.AcquireLock(ctx, lock, token, idempotencyLockTTL)
	if err != nil {
		panic(fmt.Errorf("middleware: Idempotent could not take the lock for a key: %w", err))
	}
	if !held {
		w.Header().Set("Retry-After", "1")
		refuse(w, r, http.StatusConflict, "a request with this Idempotency-Key is still being processed")
		return
	}
	// Released whatever happens below, a panic included, and on a context of
	// its own: a client that hung up must not leave its key locked.
	defer func() { _ = m.store.ReleaseLock(context.WithoutCancel(ctx), lock, token) }()

	// Read again under the lock. The first copy may have finished, stored its
	// answer and released the lock between the read above and the lock, and
	// running now would be running it twice.
	if stored, found := m.load(ctx, entry); found {
		replay(w, r, stored, fp)
		return
	}

	rec := &recordingWriter{ResponseWriter: w}
	next.ServeHTTP(rec, r)

	if rec.hijacked {
		// The handler took the connection, and what it sent on it went past
		// the recorder. There is no answer to replay, so a retry runs again.
		return
	}
	status, header := rec.status, rec.header
	if status == 0 {
		// The handler wrote nothing, which net/http sends as 200 with the
		// headers as they are now.
		status, header = http.StatusOK, keptHeaders(w.Header())
	}
	// Only 200-399 is kept, which is also what load accepts. 101 is the one
	// final status below that range: it switched this connection to another
	// protocol, and there is nothing in it a retry on a new connection could be
	// answered with, so the retry runs again.
	if status < http.StatusOK || status >= http.StatusBadRequest {
		return
	}
	encoded, err := json.Marshal(storedResponse{Fingerprint: fp, Status: status, Header: header, Body: rec.body.Bytes()})
	if err == nil {
		err = m.store.Put(context.WithoutCancel(ctx), entry, encoded, m.ttl)
	}
	if err != nil {
		observability.Log(ctx).Error("idempotency: the answer was sent and could not be kept, so a retry with the same key will run again",
			"status", status, "error", err)
	}
}

// load reads the answer stored under entry. A store that cannot answer, or an
// entry that does not decode as one, panics: the request must not run without
// knowing whether it already did.
func (m idempotency) load(ctx context.Context, entry string) (storedResponse, bool) {
	raw, err := m.store.Get(ctx, entry)
	if errors.Is(err, cache.ErrNotFound) {
		return storedResponse{}, false
	}
	if err != nil {
		panic(fmt.Errorf("middleware: Idempotent could not read the answer for a key: %w", err))
	}
	var stored storedResponse
	if err := json.Unmarshal(raw, &stored); err != nil {
		panic(fmt.Errorf("middleware: Idempotent read an answer it cannot decode: %w", err))
	}
	if stored.Status < http.StatusOK || stored.Status >= http.StatusBadRequest {
		panic(fmt.Errorf("middleware: Idempotent read an answer with status %d, and it keeps only 200-399", stored.Status))
	}
	return stored, true
}

// replay answers with the stored answer when it was stored for this request,
// and refuses when the key was used for another.
func replay(w http.ResponseWriter, r *http.Request, stored storedResponse, fp []byte) {
	if subtle.ConstantTimeCompare(stored.Fingerprint, fp) != 1 {
		refuse(w, r, http.StatusUnprocessableEntity, "this Idempotency-Key was already used with a different request")
		return
	}
	h := w.Header()
	for _, name := range replayedHeaders {
		if values := stored.Header[name]; len(values) > 0 {
			h[name] = append([]string(nil), values...)
		}
	}
	h.Set(replayedHeader, "true")
	w.WriteHeader(stored.Status)
	_, _ = w.Write(stored.Body)
}

// safeMethod reports the methods that change nothing, which have nothing to
// replay.
func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// validIdempotencyKey reports a key of 1 to maxIdempotencyKey bytes of
// printable ASCII.
func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxIdempotencyKey {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// idempotencyKeys returns the store key an answer is kept under and the key of
// the lock beside it.
//
// The tenant leads, in the clear, so a tenant's keys share a prefix; it is a
// valid tenant name, which cannot carry the separator. The subject's id and the
// client's key are hashed together, each with its length in front, so no pair
// of them can be spelled as another pair.
func idempotencyKeys(subject auth.Subject, key string) (entry, lock string) {
	h := sha256.New()
	writeField(h, []byte(subject.ID))
	writeField(h, []byte(key))
	entry = "idempotency:" + subject.Tenant + ":" + hex.EncodeToString(h.Sum(nil))
	return entry, entry + ":lock"
}

// fingerprint identifies the request a key was used for: its method, its
// address and its body.
func fingerprint(r *http.Request, body []byte) []byte {
	h := sha256.New()
	writeField(h, []byte(r.Method))
	writeField(h, []byte(r.URL.EscapedPath()))
	writeField(h, []byte(r.URL.RawQuery))
	writeField(h, body)
	return h.Sum(nil)
}

// writeField writes b to h behind its length.
func writeField(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	h.Write(n[:])
	h.Write(b)
}

// keptHeaders copies the headers on the closed list out of h.
func keptHeaders(h http.Header) map[string][]string {
	kept := map[string][]string{}
	for _, name := range replayedHeaders {
		if values := h[name]; len(values) > 0 {
			kept[name] = append([]string(nil), values...)
		}
	}
	return kept
}

// recordingWriter passes the answer through and keeps a copy of it: the
// status, the headers on the closed list as they stood when the status was
// written, and the body.
type recordingWriter struct {
	http.ResponseWriter
	status   int
	header   map[string][]string
	body     bytes.Buffer
	hijacked bool
}

func (w *recordingWriter) WriteHeader(code int) {
	// An informational status is not the answer; the final one comes after it.
	// 101 is final, by the same rule Observe applies.
	if w.status == 0 && !informational(code) {
		w.status = code
		w.header = keptHeaders(w.ResponseWriter.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.body.Write(b[:n])
	return n, err
}

// Flush keeps a streaming handler streaming through the wrapper.
func (w *recordingWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack takes the connection over through the writer underneath, and marks
// the request as one with no answer to keep.
//
// It is a method rather than left to Unwrap because http.ResponseController
// asks the outermost writer for Hijack before it unwraps, and a takeover that
// went around this writer would leave it with no status, which reads as the
// empty 200 net/http sends for a handler that wrote nothing -- and that would
// be kept and replayed to every retry.
func (w *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, brw, err
}
