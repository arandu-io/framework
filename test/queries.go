package test

import (
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/hesape/database"
	dbevents "github.com/arandu-io/hesape/database/events"
)

// AssertQueryCount fails unless the block ran exactly that many statements on
// the connection.
//
//	test.AssertQueryCount(t, conn, 1, func() {
//		_, _ = repository.Open(ctx, g)
//	})
//
// It is the assertion a listing needs and nothing else provides. The number
// exists in two other places and neither is one: the Collector counts a whole
// request, which is the wrong unit for a repository method, and the connection's
// query log is a list somebody has to turn on, read and turn off again around
// the code under test.
//
// What it catches is the loop that queries: a list of twenty rows that asks the
// database twenty-one times still returns the right page, so no assertion about
// the page can fail. The count is the only thing that changes.
//
// # It counts a block rather than being a counter
//
// A listener registered on a connection cannot be taken off it again, so a
// counter with a start and a stop would leave one behind for every test that
// used it. Bracketing a block is the shape that fits: the listener that stays
// behind stops counting when the block returns, including when the block panics,
// and a later assertion registers its own.
//
// Everything the connection ran is counted, including a statement another
// goroutine issued while the block was running. That is the honest scope of a
// connection-wide listener, and it is also the right one -- a query the code
// under test spawned is a query it made.
//
// The statements are printed on failure. "ran 21, want 2" without them is a
// number, and the answer to which twenty-one is in the list.
func AssertQueryCount(t *testing.T, connection *database.Connection, want int, run func()) {
	t.Helper()

	if connection == nil {
		t.Fatalf("counting the statements of a block: no connection to listen to")
	}
	if run == nil {
		t.Fatalf("counting the statements of a block: no block to run")
	}

	var (
		mu      sync.Mutex
		armed   bool
		counted []string
	)

	connection.Listen(func(event *dbevents.QueryExecuted) {
		if event == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if armed {
			counted = append(counted, event.SQL)
		}
	})

	mu.Lock()
	armed = true
	mu.Unlock()

	// Disarmed on the way out whichever way the block leaves, so a block that
	// panics does not leave a listener counting into the next test's statements.
	func() {
		defer func() {
			mu.Lock()
			armed = false
			mu.Unlock()
		}()
		run()
	}()

	mu.Lock()
	defer mu.Unlock()

	if len(counted) != want {
		t.Errorf("the block ran %d statement(s), want %d\n%s", len(counted), want, statements(counted))
	}
}

// statements renders what ran, for the failure message.
func statements(counted []string) string {
	if len(counted) == 0 {
		return "  (it ran none)"
	}
	var b strings.Builder
	for i, statement := range counted {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  ")
		b.WriteString(statement)
	}
	return b.String()
}
