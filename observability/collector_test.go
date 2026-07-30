package observability_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/observability"
)

// TestNilCollectorIsSafe is the production contract: outside development
// FromContext returns nil and every call must be a silent no-op. If any of these
// panicked, tracing would be a production outage waiting for a deploy.
func TestNilCollectorIsSafe(t *testing.T) {
	ctx := context.Background()
	col := observability.FromContext(ctx)
	if col != nil {
		t.Fatal("a bare context must carry no Collector")
	}

	col.RecordQuery("SELECT 1", nil, time.Millisecond, 1, nil)
	col.RecordEvent("something", nil)
	col.RecordExternal("GET", "https://example.test", 200, time.Millisecond)
	observability.Dump(ctx, "label", 42)
	observability.DumpDie(ctx, "label", 42) // must not panic without a Collector

	if got := col.SlowQueries(time.Millisecond); got != nil {
		t.Fatalf("SlowQueries on a nil Collector = %v, want nil", got)
	}
	if got := col.SuspectedNPlusOne(2); got != nil {
		t.Fatalf("SuspectedNPlusOne on a nil Collector = %v, want nil", got)
	}
	if got := col.QueryTime(); got != 0 {
		t.Fatalf("QueryTime on a nil Collector = %v, want 0", got)
	}
}

func TestRecordQueryCapturesEverything(t *testing.T) {
	col := observability.NewCollector("req-1")

	failure := errors.New("deadlock detected")
	col.RecordQuery("SELECT 1", []any{1, "two"}, 5*time.Millisecond, 3, failure)

	if len(col.Queries) != 1 {
		t.Fatalf("recorded %d queries, want 1", len(col.Queries))
	}
	q := col.Queries[0]
	if q.SQL != "SELECT 1" || q.Rows != 3 || q.Duration != 5*time.Millisecond || !errors.Is(q.Err, failure) {
		t.Fatalf("record = %+v", q)
	}
	// RecordQuery is meant to be called by the data.DB wrapper, and its stack
	// skip is calibrated for that one call path -- data.TestQueryIsRecordedWith
	// ItsOrigin is what proves the file and line are the repository's. Here it is
	// enough that a frame was captured at all.
	if q.Caller.File == "" || q.Caller.Line == 0 {
		t.Fatalf("caller = %+v, want a captured frame", q.Caller)
	}
}

func TestSuspectedNPlusOneOnlyReportsAboveThreshold(t *testing.T) {
	col := observability.NewCollector("req-1")
	for range 5 {
		col.RecordQuery("SELECT * FROM addresses WHERE user_id = $1", nil, time.Millisecond, 1, nil)
	}
	col.RecordQuery("SELECT * FROM users", nil, time.Millisecond, 10, nil)

	suspects := col.SuspectedNPlusOne(5)

	if len(suspects) != 1 {
		t.Fatalf("suspects = %v, want only the repeated statement", suspects)
	}
	if n := suspects["SELECT * FROM addresses WHERE user_id = $1"]; n != 5 {
		t.Fatalf("count = %d, want 5", n)
	}
}

func TestSlowQueriesAndQueryTime(t *testing.T) {
	col := observability.NewCollector("req-1")
	col.RecordQuery("SELECT fast", nil, 1*time.Millisecond, 1, nil)
	col.RecordQuery("SELECT slow", nil, 300*time.Millisecond, 1, nil)

	slow := col.SlowQueries(200 * time.Millisecond)
	if len(slow) != 1 || slow[0].SQL != "SELECT slow" {
		t.Fatalf("slow = %+v, want only the slow statement", slow)
	}
	if got, want := col.QueryTime(), 301*time.Millisecond; got != want {
		t.Fatalf("QueryTime = %v, want %v", got, want)
	}
}

func TestDumpRecordsOriginAndOffset(t *testing.T) {
	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	observability.Dump(ctx, "payload", map[string]int{"n": 1})

	if len(col.Dumps) != 1 {
		t.Fatalf("recorded %d dumps, want 1", len(col.Dumps))
	}
	d := col.Dumps[0]
	if d.Label != "payload" {
		t.Fatalf("label = %q", d.Label)
	}
	if !strings.HasSuffix(d.Caller.File, "collector_test.go") {
		t.Fatalf("caller = %+v, want this test file", d.Caller)
	}
	if d.At < 0 {
		t.Fatalf("offset = %v, want a non-negative offset into the request", d.At)
	}
}

func TestDumpDiePanicsWithTheRecognizedSentinel(t *testing.T) {
	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("DumpDie did not panic with a Collector installed")
		}
		if !observability.IsDumpDie(v) {
			t.Fatalf("panic value = %v, which Recover would treat as a real 500", v)
		}
		if len(col.Dumps) != 1 {
			t.Fatalf("DumpDie must record the value before aborting, got %d dumps", len(col.Dumps))
		}
	}()

	observability.DumpDie(ctx, "last thing I saw", "value")
}

func TestIsDumpDieRejectsOtherPanics(t *testing.T) {
	if observability.IsDumpDie(errors.New("real failure")) {
		t.Fatal("a real panic must not be mistaken for dump-and-die")
	}
}

func TestConcurrentRecordingIsSafe(t *testing.T) {
	col := observability.NewCollector("req-1")
	done := make(chan struct{})

	// One request can fan out to goroutines; the Collector is shared, so the
	// race detector on this test is the guarantee that sharing is safe.
	for i := range 8 {
		go func(i int) {
			col.RecordQuery("SELECT 1", nil, time.Millisecond, 1, nil)
			col.RecordEvent("event", i)
			done <- struct{}{}
		}(i)
	}
	for range 8 {
		<-done
	}

	if len(col.Queries) != 8 || len(col.Events) != 8 {
		t.Fatalf("queries = %d, events = %d, want 8 each", len(col.Queries), len(col.Events))
	}
}
