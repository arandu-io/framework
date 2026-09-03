package scheduler

import (
	"testing"
	"time"
)

// TestOccurrenceClaimNameKeepsTheExistingNamespace pins the persisted identity.
// A mixed-version rollout must have old transient locks and new durable claims
// contend on the same byte string.
func TestOccurrenceClaimNameKeepsTheExistingNamespace(t *testing.T) {
	zone := time.FixedZone("America/Sao_Paulo", -3*60*60)
	window := time.Date(2026, time.August, 3, 10, 0, 59, 0, zone)

	got := occurrenceClaimName("billing.close", "tenant-1", window)
	want := "sched:tenant-1:billing.close:1785762000"
	if got != want {
		t.Fatalf("occurrence claim name = %q, want %q", got, want)
	}
}
