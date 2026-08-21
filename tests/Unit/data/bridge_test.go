// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/hesape, against the
// code that now runs. The unit tests of the rebinder, the batch numbering, the
// transaction and the recording into the Collector belong there: they test an
// implementation this package no longer holds, and a second copy of them here
// would be a second place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- and one round trip
// per wrapper, because a wrapper is the place a call can be pointed at the
// wrong function and still compile.
//
// The other half of the proof is in grant_required_test.go, which is not a
// bridge test: it is the mandatory Grant itself, and it has to keep failing to
// compile after the move exactly as it did before it.

package unit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/database/migrations"
)

// TestAliasesAreTheHesapeSymbols is the whole of the alias half of this bridge.
//
// Every line is a compile-time assertion that the two names are one type or one
// value. A rename in hesape that this package has not followed fails here
// rather than in the thirteen repositories that import these names.
func TestAliasesAreTheHesapeSymbols(t *testing.T) {
	var (
		_ database.Dialect     = data.DialectSQLite
		_ data.Dialect         = database.DialectPostgres
		_ *database.DB         = (*data.DB)(nil)
		_ *data.DB             = (*database.DB)(nil)
		_ *database.Tx         = (*data.Tx)(nil)
		_ database.Query       = data.Query{Limit: 100}
		_ data.Query           = database.Query{Cursor: "c"}
		_ migrations.Migration = data.Migration(nil)
		_ data.Migration       = migrations.Migration(nil)
	)

	for name, pair := range map[string][2]any{
		"KeyText":         {data.KeyText, database.KeyText},
		"DialectSQLite":   {data.DialectSQLite, database.DialectSQLite},
		"DialectPostgres": {data.DialectPostgres, database.DialectPostgres},
		"DialectMySQL":    {data.DialectMySQL, database.DialectMySQL},
	} {
		if pair[0] != pair[1] {
			t.Errorf("data.%s is not the hesape value: %v against %v", name, pair[0], pair[1])
		}
	}
}

// TestWrappersKeepTheHesapeSignature is the compile-time half of the wrapper
// proof: a function with no alias form still has to have the same shape as the
// one it calls, or a caller that compiles here does not compile against hesape
// when it migrates.
func TestWrappersKeepTheHesapeSignature(t *testing.T) {
	type (
		runner   func(context.Context, *data.DB, func(context.Context) error) error
		asker    func(context.Context, *data.DB) bool
		wrapper  func(*sql.DB, data.Dialect) *data.DB
		parser   func(string) (data.Dialect, error)
		truncate func(time.Time) time.Time
		minter   func() (string, error)
		tenanter func(auth.Grant) string
	)

	var (
		_ runner   = data.Transaction
		_ runner   = database.Transaction
		_ asker    = data.InTransaction
		_ asker    = database.InTransaction
		_ wrapper  = data.Wrap
		_ wrapper  = database.Wrap
		_ parser   = data.ParseDialect
		_ parser   = database.ParseDialect
		_ truncate = data.Day
		_ truncate = database.Day
		_ minter   = data.NewID
		_ minter   = database.NewID
		_ tenanter = data.Tenant
		_ tenanter = auth.Tenant
	)
}

// TestTenantReachesAuthTenant is the one symbol that answers to another hesape
// package, so it is the one the compiler cannot check for us: data.Tenant and
// auth.Tenant have the same shape, and a bridge pointed at the wrong field
// would still build.
//
// It is also the tenant rule in one line: the value comes off the Grant.
func TestTenantReachesAuthTenant(t *testing.T) {
	g := security.SystemGrant("invoice.list", "acme")

	if got := data.Tenant(g); got != "acme" {
		t.Fatalf("data.Tenant = %q, want the tenant the Grant carries", got)
	}
	if data.Tenant(g) != auth.Tenant(g) {
		t.Fatal("data.Tenant and auth.Tenant disagree about the same Grant")
	}
	if got := data.Tenant(security.Grant{}); got != "" {
		t.Fatalf("the zero Grant answers %q; it has no tenant to give", got)
	}
}

// TestWrapReachesHesape proves the constructor calls through rather than
// building a handle of its own: the empty dialect means SQLite, and that
// default is hesape's to apply.
func TestWrapReachesHesape(t *testing.T) {
	if got := data.Wrap(nil, "").Dialect(); got != data.DialectSQLite {
		t.Fatalf("Wrap with no dialect = %q, want the SQLite default", got)
	}
	if got := data.Wrap(nil, data.DialectPostgres).Dialect(); got != database.DialectPostgres {
		t.Fatalf("Wrap kept %q rather than the dialect it was given", got)
	}
}

// TestTransactionReachesHesape proves the wrapper calls through, by the one
// answer it can give without a database: the refusal, whose message can only
// come from the hesape package.
func TestTransactionReachesHesape(t *testing.T) {
	err := data.Transaction(context.Background(), nil, func(context.Context) error {
		t.Fatal("the callback ran without a database handle")
		return nil
	})
	if err == nil {
		t.Fatal("Transaction accepted a nil handle")
	}
	if !strings.Contains(err.Error(), "database: Transaction needs a database handle") {
		t.Fatalf("the refusal did not come from hesape: %v", err)
	}
}

// TestParseDialectAndDayReachHesape: two plain functions with no alias form,
// each proved by a value only the hesape implementation produces.
func TestParseDialectAndDayReachHesape(t *testing.T) {
	d, err := data.ParseDialect("pgsql")
	if err != nil || d != database.DialectPostgres {
		t.Fatalf("ParseDialect(pgsql) = %q, %v", d, err)
	}
	if _, err := data.ParseDialect("oracle"); err == nil {
		t.Fatal("ParseDialect accepted an engine the framework does not speak")
	}

	// Late afternoon UTC, written in a zone three hours behind it, so a Day
	// that forgot to convert would answer the day before.
	afternoon := time.Date(2026, 8, 14, 12, 30, 0, 0, time.FixedZone("BRT", -3*60*60))
	if got := data.Day(afternoon); !got.Equal(time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Day = %v, want midnight UTC of the UTC day", got)
	}
}

// TestNewIDReachesHesape asks only what the bridge is responsible for: that an
// identifier comes back at all, in the shape a KeyText column takes.
func TestNewIDReachesHesape(t *testing.T) {
	id, err := data.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("NewID = %q, want a hyphenated UUID", id)
	}
}

// TestRepositoryKeepsTheOldListShape is the envelope half of this bridge, and
// the reason Repository is declared here instead of aliased.
//
// A repository written before the move -- with security.Grant in the second
// position and List answering ([]T, error) -- still satisfies the contract,
// which is what the `var _ data.Repository[T, string] = (*R)(nil)` line in
// every generated module asserts. If this file stops compiling, so do
// framework/modules/auth, the aru templates and the three repositories in
// examples.
func TestRepositoryKeepsTheOldListShape(t *testing.T) {
	var repo data.Repository[row, string] = (*oldRepo)(nil)

	if _, err := repo.List(context.Background(), security.Grant{}, data.Query{}); err == nil {
		t.Fatal("the contract handed a zero Grant to a repository that must refuse it")
	}
}

type row struct{ ID string }

const actionRowView security.Action = "row.view"

// oldRepo is a repository written against the pre-move contract: the Grant is
// security.Grant, which the security bridge aliases to auth.Grant, and List
// answers a slice rather than a database.Page.
type oldRepo struct{}

func (*oldRepo) Find(ctx context.Context, g security.Grant, id string) (row, error) {
	return row{}, g.Check(actionRowView)
}

func (*oldRepo) List(ctx context.Context, g security.Grant, q data.Query) ([]row, error) {
	return nil, g.Check(actionRowView)
}

func (*oldRepo) Create(ctx context.Context, g security.Grant, entity row) (row, error) {
	return row{}, g.Check(actionRowView)
}

func (*oldRepo) Update(ctx context.Context, g security.Grant, entity row) (row, error) {
	return row{}, g.Check(actionRowView)
}

func (*oldRepo) Delete(ctx context.Context, g security.Grant, id string) error {
	return g.Check(actionRowView)
}
