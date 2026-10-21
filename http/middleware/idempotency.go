// Idempotency keys, answered by github.com/arandu-io/hesape/http/middleware.

package middleware

import (
	"net/http"
	"time"

	hmiddleware "github.com/arandu-io/hesape/http/middleware"
)

// IdempotencyStore is where Idempotent keeps the first answer to a key, and
// the lock that stops two copies of one request running at once. A hesape
// cache store that can hold a lock satisfies it as it stands.
type IdempotencyStore = hmiddleware.IdempotencyStore

// Idempotent makes a write that carries an Idempotency-Key header run once,
// and answers every retry of it with the answer the first copy got, for ttl.
// The key is scoped to the subject on the request, so two accounts sending
// the same key never see each other's answer.
func Idempotent(store IdempotencyStore, ttl time.Duration) func(http.Handler) http.Handler {
	return hmiddleware.Idempotent(store, ttl)
}
