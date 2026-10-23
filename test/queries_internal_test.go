package test

import (
	"context"
	"testing"
)

// The assertion a listing needs: a page that asks the database once per row
// still renders the right page, so nothing about the page can fail. The count
// is the only thing that changes.
func TestAssertQueryCountCountsWhatTheBlockRan(t *testing.T) {
	connection, _ := newFakeConnection(t)
	ctx := context.Background()

	AssertQueryCount(t, connection, 3, func() {
		for range 3 {
			if _, err := connection.Select(ctx, "SELECT COUNT(*) FROM invoices", nil, false); err != nil {
				t.Fatalf("selecting: %v", err)
			}
		}
	})
}

// A block that touches no database ran no statements, which is an assertion
// worth making: it is how a test says a second call was served from the cache.
func TestAssertQueryCountAcceptsABlockThatAsksNothing(t *testing.T) {
	connection, _ := newFakeConnection(t)

	AssertQueryCount(t, connection, 0, func() {})
}

// The count is of the block and not of the test. A listener cannot be taken off
// a connection again, so what is left behind after the block has to stop
// counting -- otherwise the second assertion in a test would include the
// statements of the first.
func TestAssertQueryCountIgnoresWhatRanOutsideTheBlock(t *testing.T) {
	connection, _ := newFakeConnection(t)
	ctx := context.Background()

	ask := func() {
		if _, err := connection.Select(ctx, "SELECT COUNT(*) FROM invoices", nil, false); err != nil {
			t.Fatalf("selecting: %v", err)
		}
	}

	ask()
	AssertQueryCount(t, connection, 1, ask)
	ask()
	AssertQueryCount(t, connection, 1, ask)
}

// A block that panics still disarms, or the listener it left behind counts the
// rest of the package's statements into a number nobody reads.
func TestAssertQueryCountStopsCountingWhenTheBlockPanics(t *testing.T) {
	connection, _ := newFakeConnection(t)
	ctx := context.Background()

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the block did not panic, so this proves nothing")
			}
		}()
		AssertQueryCount(t, connection, 0, func() { panic("the code under test blew up") })
	}()

	AssertQueryCount(t, connection, 1, func() {
		if _, err := connection.Select(ctx, "SELECT COUNT(*) FROM invoices", nil, false); err != nil {
			t.Fatalf("selecting: %v", err)
		}
	})
}

// The failure has to say which statements ran. A bare "ran 21, want 2" is a
// number, and the answer to which twenty-one is the list.
func TestTheFailureNamesTheStatementsThatRan(t *testing.T) {
	got := statements([]string{"SELECT * FROM invoices", "SELECT * FROM lines WHERE invoice_id = ?"})

	want := "  SELECT * FROM invoices\n  SELECT * FROM lines WHERE invoice_id = ?"
	if got != want {
		t.Errorf("printed\n%s\nwant\n%s", got, want)
	}
}
