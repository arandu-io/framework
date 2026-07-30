package data_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
)

func TestQueryIsRecordedWithItsOrigin(t *testing.T) {
	sqldb, _ := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb)

	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	rows, err := db.QueryContext(ctx, `SELECT id FROM users WHERE tenant_id = $1`, "t1")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	rows.Close()

	if len(col.Queries) != 1 {
		t.Fatalf("recorded %d queries, want 1", len(col.Queries))
	}
	q := col.Queries[0]
	if !strings.Contains(q.SQL, "FROM users") {
		t.Fatalf("recorded SQL = %q", q.SQL)
	}
	if len(q.Args) != 1 || q.Args[0] != "t1" {
		t.Fatalf("recorded args = %v, want [t1]", q.Args)
	}
	// The caller is the point of the whole feature: a query list without file
	// and line does not tell you where the N+1 lives.
	if !strings.HasSuffix(q.Caller.File, "db_test.go") {
		t.Fatalf("caller file = %q, want this test file", q.Caller.File)
	}
	if q.Caller.Line == 0 {
		t.Fatal("caller line was not captured")
	}
}

func TestExecRecordsAffectedRows(t *testing.T) {
	sqldb, _ := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb)

	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, "u1"); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}

	if len(col.Queries) != 1 || col.Queries[0].Rows != 1 {
		t.Fatalf("recorded = %+v, want one record with Rows=1", col.Queries)
	}
}

func TestQueryRecordsTheError(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.failOn = "FROM users"
	state.failErr = errors.New("relation \"users\" does not exist")
	db := data.Wrap(sqldb)

	col := observability.NewCollector("req-1")
	ctx := observability.WithCollector(context.Background(), col)

	if _, err := db.QueryContext(ctx, `SELECT id FROM users`); err == nil {
		t.Fatal("QueryContext succeeded, want the driver error")
	}

	if len(col.Queries) != 1 || col.Queries[0].Err == nil {
		t.Fatalf("the failed query must be recorded with its error, got %+v", col.Queries)
	}
}

// TestRecordingIsFreeInProduction is the zero cost claim: with no Collector in
// the context, every Record call must be a no-op on a nil receiver.
func TestRecordingIsFreeInProduction(t *testing.T) {
	sqldb, _ := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb)

	ctx := context.Background()
	if observability.FromContext(ctx) != nil {
		t.Fatal("a bare context must carry no Collector")
	}

	rows, err := db.QueryContext(ctx, `SELECT id FROM users`)
	if err != nil {
		t.Fatalf("QueryContext without a Collector: %v", err)
	}
	rows.Close()
}

func TestTenantComesFromTheGrant(t *testing.T) {
	g := security.SystemGrant("test.view", "tenant-42")

	if got := data.Tenant(g); got != "tenant-42" {
		t.Fatalf("Tenant = %q, want tenant-42", got)
	}
}
