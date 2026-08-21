package foundation

import (
	"context"
	"errors"
	"testing"
)

// fakeConnection stands in for a key-value connection.
//
// It is what the structural interface is for: this satisfies CacheConnection
// without either side importing the other, which is the same reason a real
// connection does.
type fakeConnection struct {
	pingErr  error
	closeErr error
	pings    int
	closes   int
}

func (c *fakeConnection) Ping(context.Context) error {
	c.pings++
	return c.pingErr
}

func (c *fakeConnection) Close() error {
	c.closes++
	return c.closeErr
}

// TestTheModuleAnswersForTheConnectionItHolds is the whole point of the type:
// a store that is down has to say so where the database says so.
//
// Both directions matter. A module that always answers healthy is worse than no
// module: it puts a green line on the page for a store nobody can reach.
func TestTheModuleAnswersForTheConnectionItHolds(t *testing.T) {
	down := errors.New("connection refused")
	conn := &fakeConnection{pingErr: down}
	m := NewCacheModule("cache", conn)

	if err := m.Health(context.Background()); !errors.Is(err, down) {
		t.Errorf("Health hid the refusal: got %v, want %v", err, down)
	}
	if conn.pings != 1 {
		t.Errorf("Health asked the connection %d times, want 1", conn.pings)
	}

	conn.pingErr = nil
	if err := m.Health(context.Background()); err != nil {
		t.Errorf("Health reported a reachable store as down: %v", err)
	}
}

// TestCloseReturnsThePool covers the half that has no symptom until it is too
// late: a connection nothing closes leaks its pool for the life of the process.
func TestCloseReturnsThePool(t *testing.T) {
	conn := &fakeConnection{}
	m := NewCacheModule("cache", conn)

	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if conn.closes != 1 {
		t.Errorf("Close reached the connection %d times, want 1", conn.closes)
	}

	failing := errors.New("already closed")
	m = NewCacheModule("cache", &fakeConnection{closeErr: failing})
	if err := m.Close(context.Background()); !errors.Is(err, failing) {
		t.Errorf("Close swallowed the failure: got %v, want %v", err, failing)
	}
}

// TestTheNameIsUsedAsGiven pins the identifier, because it is what tells two
// stores apart on a health report.
func TestTheNameIsUsedAsGiven(t *testing.T) {
	for _, name := range []string{"cache", "sessions", "rate-limit"} {
		if got := NewCacheModule(name, &fakeConnection{}).Name(); got != name {
			t.Errorf("Name() = %q, want %q", got, name)
		}
	}
}

// TestItRegistersNoRoutes states the absence, so that adding one later is a
// decision somebody makes rather than a line that appears.
func TestItRegistersNoRoutes(t *testing.T) {
	// A nil router is safe precisely because nothing is registered. If Routes
	// ever touches its argument, this panics rather than passing quietly.
	NewCacheModule("cache", &fakeConnection{}).Routes(nil)
}
