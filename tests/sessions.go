package tests

import (
	"context"
	"sync"
	"time"

	"github.com/arandu-io/framework/security"
	hhttp "github.com/arandu-io/hesape/http"
)

// accounts is the account table behind one session store: the subject each
// Enroll last stored, by tenant and id.
type accounts struct {
	mu   sync.Mutex
	byID map[[2]string]security.Subject
}

// directories maps each store NewSessions built to its account table, so a
// helper handed only the store can enroll somebody in it.
var directories sync.Map // *security.SessionStore -> *accounts

// NewSessions returns a session store kept in memory whose subjects are
// resolved from an account table of its own, the way an application resolves
// them from its users.
//
// A session keeps the identity it was opened for and not the roles: Load asks
// the resolver who the account is now, and a store with no resolver refuses a
// subject that carries roles or actions. Enroll is what puts somebody in the
// table, and Start calls nothing for the test -- enroll first, then open the
// session.
func NewSessions(appKey []byte, ttl time.Duration) *security.SessionStore {
	store := security.NewSessionStore(appKey, ttl, false, security.NewMemoryBackend())
	table := &accounts{byID: map[[2]string]security.Subject{}}
	store.ResolveSubjects(func(_ context.Context, who security.Subject) (security.Subject, error) {
		table.mu.Lock()
		defer table.mu.Unlock()
		sub, ok := table.byID[[2]string{who.Tenant, who.ID}]
		if !ok {
			return security.Subject{}, hhttp.ErrSubjectGone
		}
		return sub, nil
	})
	directories.Store(store, table)
	return store
}

// Enroll records sub as the account its id and tenant name in the table of a
// store NewSessions built, replacing what was there. It panics for a store
// NewSessions did not build, because a session opened on that one would be
// refused for a reason the test never wrote.
func Enroll(store *security.SessionStore, sub security.Subject) {
	v, ok := directories.Load(store)
	if !ok {
		panic("tests: Enroll was handed a session store NewSessions did not build")
	}
	table := v.(*accounts)
	table.mu.Lock()
	defer table.mu.Unlock()
	table.byID[[2]string{sub.Tenant, sub.ID}] = sub
}
