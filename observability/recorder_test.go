package observability_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/arandu-io/framework/observability"
)

func recorded(id string, at time.Time) observability.Recorded {
	return observability.Recorded{
		RequestID: id,
		Method:    "GET",
		Path:      "/customers",
		Status:    200,
		Duration:  10 * time.Millisecond,
		At:        at,
		Collector: observability.NewCollector(id),
	}
}

func TestRecentReturnsNewestFirst(t *testing.T) {
	r := observability.NewRecorder(10)
	now := time.Now()
	for i := range 3 {
		r.Record(recorded(fmt.Sprintf("req-%d", i), now.Add(time.Duration(i)*time.Second)))
	}

	got := r.Recent(0)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	for i, want := range []string{"req-2", "req-1", "req-0"} {
		if got[i].RequestID != want {
			t.Errorf("entry %d = %s, want %s", i, got[i].RequestID, want)
		}
	}
}

// TestTheRingDropsTheOldest is what keeps a long dev session from ending in an
// out-of-memory kill -- which would happen at the end of the session, exactly
// when losing the process costs the most.
func TestTheRingDropsTheOldest(t *testing.T) {
	r := observability.NewRecorder(3)
	now := time.Now()
	for i := range 5 {
		r.Record(recorded(fmt.Sprintf("req-%d", i), now))
	}

	got := r.Recent(0)
	if len(got) != 3 {
		t.Fatalf("the buffer holds %d entries, want 3", len(got))
	}
	for i, want := range []string{"req-4", "req-3", "req-2"} {
		if got[i].RequestID != want {
			t.Errorf("entry %d = %s, want %s", i, got[i].RequestID, want)
		}
	}
	if _, found := r.Find("req-0"); found {
		t.Error("an entry past the ring size is still reachable")
	}
}

// TestFindAfterWrapping: the id lookup has to walk the ring the same way the
// list does, or `aru trace` finds nothing for a request the console shows.
func TestFindAfterWrapping(t *testing.T) {
	r := observability.NewRecorder(3)
	for i := range 7 {
		r.Record(recorded(fmt.Sprintf("req-%d", i), time.Now()))
	}

	for _, id := range []string{"req-6", "req-5", "req-4"} {
		if _, found := r.Find(id); !found {
			t.Errorf("%s is in the buffer and Find missed it", id)
		}
	}
	if _, found := r.Find("req-3"); found {
		t.Error("Find returned an entry the ring had already dropped")
	}
}

func TestRecentRespectsTheLimit(t *testing.T) {
	r := observability.NewRecorder(10)
	for i := range 5 {
		r.Record(recorded(fmt.Sprintf("req-%d", i), time.Now()))
	}

	if got := r.Recent(2); len(got) != 2 {
		t.Fatalf("Recent(2) returned %d entries", len(got))
	}
	if got := r.Recent(50); len(got) != 5 {
		t.Fatalf("Recent(50) returned %d entries, want the 5 that exist", len(got))
	}
}

// TestANilRecorderIsSafe: production has no recorder, and the middleware must
// not have to check before every call.
func TestANilRecorderIsSafe(t *testing.T) {
	var r *observability.Recorder

	r.Record(recorded("req-1", time.Now()))
	if got := r.Recent(0); got != nil {
		t.Errorf("Recent on a nil recorder = %v", got)
	}
	if _, found := r.Find("req-1"); found {
		t.Error("Find on a nil recorder found something")
	}
	if r.Len() != 0 {
		t.Error("Len on a nil recorder is not zero")
	}
}

func TestTheTimelineAddsUp(t *testing.T) {
	c := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), c)

	c.RecordQuery("SELECT 1", nil, 30*time.Millisecond, 1, nil)
	c.RecordQuery("SELECT 2", nil, 20*time.Millisecond, 1, nil)
	c.RecordRender("customer/list.templ", 10*time.Millisecond)
	c.RecordExternal("GET", "https://api.example/x", 200, 25*time.Millisecond)
	_ = ctx

	timeline := c.Timeline(100 * time.Millisecond)
	if timeline.SQL != 50*time.Millisecond {
		t.Errorf("SQL = %s, want 50ms", timeline.SQL)
	}
	if timeline.Render != 10*time.Millisecond {
		t.Errorf("Render = %s, want 10ms", timeline.Render)
	}
	if timeline.External != 25*time.Millisecond {
		t.Errorf("External = %s, want 25ms", timeline.External)
	}
	if timeline.Other != 15*time.Millisecond {
		t.Errorf("Other = %s, want 15ms", timeline.Other)
	}
	if got := timeline.Percent(timeline.SQL); got != 50 {
		t.Errorf("SQL is %d%% of the request, want 50", got)
	}
}

// TestConcurrentWorkDoesNotProduceNegativeTime: queries issued from parallel
// goroutines overlap in wall clock, so the parts can exceed the total. A
// negative "other" would read like a bug in the framework rather than like
// concurrency.
func TestConcurrentWorkDoesNotProduceNegativeTime(t *testing.T) {
	c := observability.NewCollector("req-1")
	c.RecordQuery("SELECT 1", nil, 80*time.Millisecond, 1, nil)
	c.RecordQuery("SELECT 2", nil, 80*time.Millisecond, 1, nil)

	timeline := c.Timeline(100 * time.Millisecond)
	if timeline.Other < 0 {
		t.Fatalf("Other = %s", timeline.Other)
	}
}

func TestTimelineOnANilCollector(t *testing.T) {
	var c *observability.Collector

	timeline := c.Timeline(50 * time.Millisecond)
	if timeline.Total != 50*time.Millisecond || timeline.Other != 50*time.Millisecond {
		t.Fatalf("timeline = %+v", timeline)
	}
	if timeline.Percent(timeline.Other) != 100 {
		t.Error("a request with no collector should be entirely unaccounted for")
	}
}

func TestPercentOfAZeroLengthRequest(t *testing.T) {
	timeline := observability.Timeline{}
	if got := timeline.Percent(time.Millisecond); got != 0 {
		t.Fatalf("Percent = %d on a request with no duration, want 0", got)
	}
}

func TestStatusClass(t *testing.T) {
	for status, want := range map[int]string{
		200: "ok", 204: "ok", 302: "redirect",
		404: "client-error", 422: "client-error", 500: "server-error", 503: "server-error",
	} {
		got := observability.Recorded{Status: status}.StatusClass()
		if got != want {
			t.Errorf("status %d = %q, want %q", status, got, want)
		}
	}
}

// TestTheRecorderIsSafeUnderConcurrentRequests: it is written by every request
// goroutine and read by the console at the same time.
func TestTheRecorderIsSafeUnderConcurrentRequests(t *testing.T) {
	r := observability.NewRecorder(50)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := range 200 {
			r.Record(recorded(fmt.Sprintf("req-%d", i), time.Now()))
		}
	}()
	for range 200 {
		_ = r.Recent(10)
		_, _ = r.Find("req-5")
		_ = r.Len()
	}
	<-done
}

// TestTheQueryErrorSurvives: a query that failed is the one worth looking at,
// and dropping the error would make the console show it as an ordinary row.
func TestTheQueryErrorSurvives(t *testing.T) {
	c := observability.NewCollector("req-1")
	c.RecordQuery("SELECT 1", nil, time.Millisecond, 0, errors.New("no such table: customer"))

	if len(c.Queries) != 1 || c.Queries[0].Err == nil {
		t.Fatal("the query error was not recorded")
	}
}

// TestRecordingCostsNothingWithTracingOff is exit criterion 2 of phase 3, as a
// test rather than as a benchmark somebody has to remember to run.
//
// The claim is not "low cost in production". It is zero: with the Collector
// absent, every Record is a method on a nil receiver that returns before
// touching anything, so instrumented code allocates exactly as much as
// uninstrumented code.
//
// If this ever fails, something started allocating before the nil check -- a
// variadic parameter, a fmt call in an argument, a defer. That is the bug this
// test exists to catch, and it is the kind that never shows up in development,
// where the Collector is always on.
func TestRecordingCostsNothingWithTracingOff(t *testing.T) {
	var col *observability.Collector

	// The arguments exist beforehand, which is what the real path does:
	// data.DB passes the same slice it just handed to the driver.
	args := []any{"c-1"}
	payload := map[string]any{"id": "c-1"}

	allocs := testing.AllocsPerRun(200, func() {
		col.RecordQuery("SELECT * FROM invoice WHERE customer_id = ?", args, time.Millisecond, 1, nil)
		col.RecordEvent("customer.created", payload)
		col.RecordExternal("GET", "https://api.example/rates", 200, time.Millisecond)
		col.RecordRender("customer/list.templ", time.Millisecond)
	})

	if allocs != 0 {
		t.Fatalf("recording with tracing off allocated %v times per run, want 0", allocs)
	}
}

// TestReadingCostsNothingWithTracingOff: the middleware asks these on every
// request, including in production.
func TestReadingCostsNothingWithTracingOff(t *testing.T) {
	var col *observability.Collector

	allocs := testing.AllocsPerRun(200, func() {
		_ = col.QueryTime()
		_ = col.SlowQueries(100 * time.Millisecond)
		_ = col.SuspectedNPlusOne(5)
	})

	if allocs != 0 {
		t.Fatalf("reading with tracing off allocated %v times per run, want 0", allocs)
	}
}
