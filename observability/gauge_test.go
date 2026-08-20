// Tests of the gauge registry, which is the one thing in this package that is
// not a bridge and so is the one thing tested here for what it does.

package observability_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/arandu-io/framework/observability"
)

// TestSetKeepsOnlyTheLastValue is the assertion the whole design rests on.
//
// Two writes and one read: the read is the second value, and the first is not
// reachable from anywhere -- not under the name, not under another name, not in
// the enumeration. A registry that kept it would be holding a series, and a
// series is the thing this is not.
func TestSetKeepsOnlyTheLastValue(t *testing.T) {
	const (
		first  = int64(7)
		second = int64(11)
	)
	name := observability.GaugeName{Metric: "connections", Tenant: "acme"}

	g := observability.NewGauges()
	g.Set(name, first)
	g.Set(name, second)

	got, ok := g.Read(name)
	if !ok {
		t.Fatal("the name was set twice and reads as unset")
	}
	if got != second {
		t.Errorf("Read answered %d, want the second write, %d", got, second)
	}

	names := g.Names()
	if len(names) != 1 {
		t.Fatalf("two writes to one name left %d names, want 1: %v", len(names), names)
	}
	for _, n := range names {
		value, _ := g.Read(n)
		if value == first {
			t.Errorf("%v still holds the replaced value %d", n, first)
		}
	}
}

// TestReadSeparatesZeroFromUnset covers the second result, which is the only
// thing standing between a number that is zero and a number nobody has written.
func TestReadSeparatesZeroFromUnset(t *testing.T) {
	name := observability.GaugeName{Metric: "connections", Tenant: "acme"}

	g := observability.NewGauges()
	if _, ok := g.Read(name); ok {
		t.Error("an untouched name reads as set")
	}

	g.Set(name, 0)

	value, ok := g.Read(name)
	if !ok {
		t.Error("a name set to zero reads as unset")
	}
	if value != 0 {
		t.Errorf("Read answered %d, want 0", value)
	}
}

// TestNameIsMetricAndTenantTogether proves the key is the pair. One tenant's
// number overwriting another's would be the failure that looks like a quiet
// deployment.
func TestNameIsMetricAndTenantTogether(t *testing.T) {
	acme := observability.GaugeName{Metric: "connections", Tenant: "acme"}
	globex := observability.GaugeName{Metric: "connections", Tenant: "globex"}
	channels := observability.GaugeName{Metric: "channels", Tenant: "acme"}

	g := observability.NewGauges()
	g.Set(acme, 3)
	g.Set(globex, 5)
	g.Set(channels, 9)

	for name, want := range map[observability.GaugeName]int64{acme: 3, globex: 5, channels: 9} {
		got, ok := g.Read(name)
		if !ok {
			t.Fatalf("%v reads as unset", name)
		}
		if got != want {
			t.Errorf("%v answered %d, want %d", name, got, want)
		}
	}
}

// TestNamesAreSortedByMetricThenTenant covers the order, which callers rely on
// to draw a table twice and get the same rows in the same places.
func TestNamesAreSortedByMetricThenTenant(t *testing.T) {
	g := observability.NewGauges()
	g.Set(observability.GaugeName{Metric: "connections", Tenant: "globex"}, 1)
	g.Set(observability.GaugeName{Metric: "channels", Tenant: "zenith"}, 1)
	g.Set(observability.GaugeName{Metric: "connections", Tenant: "acme"}, 1)
	g.Set(observability.GaugeName{Metric: "channels", Tenant: "acme"}, 1)

	want := []observability.GaugeName{
		{Metric: "channels", Tenant: "acme"},
		{Metric: "channels", Tenant: "zenith"},
		{Metric: "connections", Tenant: "acme"},
		{Metric: "connections", Tenant: "globex"},
	}

	got := g.Names()
	if len(got) != len(want) {
		t.Fatalf("Names answered %d names, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d is %v, want %v", i, got[i], want[i])
		}
	}
}

// TestNamesOfAnEmptyRegistryIsEmpty covers the case a caller draws before
// anything has been measured, which is every process for its first moment.
func TestNamesOfAnEmptyRegistryIsEmpty(t *testing.T) {
	if got := observability.NewGauges().Names(); len(got) != 0 {
		t.Errorf("a new registry answered %d names, want none: %v", len(got), got)
	}
}

// TestConcurrentUse is what -race reads. Every method may be called from any
// goroutine at any time, because what writes a gauge is usually whatever is
// holding the thing being measured.
func TestConcurrentUse(t *testing.T) {
	g := observability.NewGauges()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(3)

		name := observability.GaugeName{Metric: "connections", Tenant: "tenant-" + strconv.Itoa(i)}
		go func() {
			defer wg.Done()
			for v := range int64(100) {
				g.Set(name, v)
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				g.Read(name)
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				g.Names()
			}
		}()
	}
	wg.Wait()

	if got := len(g.Names()); got != 8 {
		t.Errorf("eight tenants were written and %d names came back", got)
	}
}
