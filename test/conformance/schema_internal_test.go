package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth/account"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// What a database decides -- the columns a migration made, the unique index
// that refuses an address twice, the precision an instant keeps -- is proved
// against real engines by the applications and connectors that call Schema.
// What is proved here is the reading of those answers: which catalogs pass,
// which columns compare equal across engines' spellings, and what an instant
// reads back as.

type reversible struct {
	migrations.BaseMigration
	name string
}

func (m reversible) GetName() string                                 { return m.name }
func (reversible) Up(context.Context, migrations.Connection) error   { return nil }
func (reversible) Down(context.Context, migrations.Connection) error { return nil }

type forwardOnly struct {
	migrations.BaseMigration
	name string
}

func (m forwardOnly) GetName() string                               { return m.name }
func (forwardOnly) Up(context.Context, migrations.Connection) error { return nil }

func TestTheCatalogPassesTheNativeMigrationsInOrderAmongOthers(t *testing.T) {
	wanted := []string{"0001_users", "0002_names", "0003_two_factor"}
	catalog := []migrations.Migration{
		reversible{name: "0001_users"}, reversible{name: "0001_invoices"},
		reversible{name: "0002_names"}, reversible{name: "0003_two_factor"}, reversible{name: "0009_comments"},
	}
	if problems := catalogProblems(catalog, wanted); len(problems) != 0 {
		t.Fatalf("a catalog with domain migrations beside the native ones failed: %v", problems)
	}
}

func TestTheCatalogFailsAMissingAReorderedOrAnIrreversibleMigration(t *testing.T) {
	wanted := []string{"0001_users", "0002_names", "0003_two_factor"}
	for name, c := range map[string]struct {
		catalog []migrations.Migration
		want    string
	}{
		"missing": {
			[]migrations.Migration{reversible{name: "0001_users"}, reversible{name: "0002_names"}},
			"holds the native auth migrations [0001_users 0002_names], want [0001_users 0002_names 0003_two_factor] in that order",
		},
		"reordered": {
			[]migrations.Migration{reversible{name: "0002_names"}, reversible{name: "0001_users"}, reversible{name: "0003_two_factor"}},
			"holds the native auth migrations [0002_names 0001_users 0003_two_factor]",
		},
		"irreversible": {
			[]migrations.Migration{reversible{name: "0001_users"}, forwardOnly{name: "0002_names"}, reversible{name: "0003_two_factor"}},
			"the native auth migration 0002_names cannot be rolled back",
		},
	} {
		t.Run(name, func(t *testing.T) {
			problems := catalogProblems(c.catalog, wanted)
			if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Fatalf("catalogProblems = %q, want one saying %q", problems, c.want)
			}
		})
	}
}

func TestEveryEnginesSpellingReadsAsOneFamily(t *testing.T) {
	for _, c := range []struct{ typeName, full, want string }{
		{"varchar", "varchar(255)", "varchar"},
		{"", "character varying(255)", "varchar"},
		{"character varying", "", "varchar"},
		{"text", "text", "text"},
		{"mediumtext", "", "text"},
		{"datetime", "datetime", "time"},
		{"timestamp", "timestamp(6) without time zone", "time"},
		{"timestamptz", "", "time"},
		{"integer", "", "integer"},
		{"int8", "", "integer"},
		{"bigint", "bigint(20)", "integer"},
	} {
		if got := typeFamily(c.typeName, c.full); got != c.want {
			t.Errorf("typeFamily(%q, %q) = %q, want %q", c.typeName, c.full, got, c.want)
		}
	}
}

func TestTheUsersTableIsASubsetAndTheSecondFactorsTablesAreExact(t *testing.T) {
	users := []schema.ColumnInfo{
		{Name: "id", TypeName: "varchar"}, {Name: "tenant_id", TypeName: "varchar"}, {Name: "email", TypeName: "varchar"},
		{Name: "password", TypeName: "text"}, {Name: "roles", TypeName: "text"}, {Name: "created_at", TypeName: "datetime"},
		{Name: "name", TypeName: "varchar", Nullable: true}, {Name: "verified_at", TypeName: "datetime", Nullable: true},
	}
	if problems := columnProblems("users", users, usersColumns, false); len(problems) != 0 {
		t.Fatalf("the skeleton's users table failed: %v", problems)
	}
	// A name the application chose to require is the application's call.
	users[6].Nullable = false
	if problems := columnProblems("users", users, usersColumns, false); len(problems) != 0 {
		t.Fatalf("a required name failed: %v", problems)
	}
	users[7].Nullable = false
	if problems := columnProblems("users", users, usersColumns, false); len(problems) != 1 || !strings.Contains(problems[0], "users.verified_at nullable = false, want true") {
		t.Fatalf("a verified_at that refuses NULL passed: %v", problems)
	}

	twoFactor := []schema.ColumnInfo{
		{Name: "user_id", TypeName: "varchar"}, {Name: "tenant_id", TypeName: "varchar"}, {Name: "secret", TypeName: "text"},
		{Name: "confirmed_at", TypeName: "timestamp", Nullable: true}, {Name: "last_used_step", TypeName: "int8"},
		{Name: "created_at", TypeName: "timestamp"},
	}
	if problems := columnProblems("user_two_factor", twoFactor, twoFactorColumns, true); len(problems) != 0 {
		t.Fatalf("the second factor's table failed: %v", problems)
	}
	for name, c := range map[string]struct {
		columns []schema.ColumnInfo
		want    string
	}{
		"missing column": {twoFactor[:5], "user_two_factor has no created_at column"},
		"wrong family":   {replace(twoFactor, 2, schema.ColumnInfo{Name: "secret", TypeName: "varchar"}), "user_two_factor.secret is a varchar column (varchar), want text"},
		"extra column":   {append(append([]schema.ColumnInfo(nil), twoFactor...), schema.ColumnInfo{Name: "label", TypeName: "text"}), "has columns [user_id tenant_id secret confirmed_at last_used_step created_at label], want exactly"},
		"nullable":       {replace(twoFactor, 4, schema.ColumnInfo{Name: "last_used_step", TypeName: "int8", Nullable: true}), "user_two_factor.last_used_step nullable = true, want false"},
	} {
		t.Run(name, func(t *testing.T) {
			problems := columnProblems("user_two_factor", c.columns, twoFactorColumns, true)
			if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Fatalf("columnProblems = %q, want one saying %q", problems, c.want)
			}
		})
	}
}

func replace(columns []schema.ColumnInfo, i int, c schema.ColumnInfo) []schema.ColumnInfo {
	out := append([]schema.ColumnInfo(nil), columns...)
	out[i] = c
	return out
}

func TestAnInstantReadsBackFromEveryShapeADriverHandsIt(t *testing.T) {
	want := time.Date(2026, 10, 19, 12, 34, 56, 123456000, time.UTC)
	for _, stored := range []any{
		want,
		"2026-10-19 12:34:56.123456+00:00",
		[]byte("2026-10-19T12:34:56.123456Z"),
		"2026-10-19 12:34:56.123456",
	} {
		got, err := asTime(stored)
		if err != nil || !got.Equal(want) {
			t.Errorf("asTime(%v) = %v, %v; want %v", stored, got, err, want)
		}
	}
	if _, err := asTime(42); err == nil {
		t.Error("an integer read back as an instant")
	}
	if _, err := asTime("yesterday"); err == nil {
		t.Error("a word read back as an instant")
	}
}

func TestTheNativeMigrationsAreTheUsersAndThenTheSecondFactors(t *testing.T) {
	users := []migrations.Migration{reversible{name: "0001_users"}}
	names := migrationNames(append(users, account.Migrations()...))
	if names[0] != "0001_users" || names[1] != "20260828_0003_create_two_factor" || names[2] != "20261019_0004_widen_two_factor_time_precision" {
		t.Fatalf("the migrations Schema runs are %v", names)
	}
}

func init() {
	broken["Schema/no fixture"] = brokenCase{
		run:  func(t *testing.T) { Schema(t, SchemaFixture{}) },
		want: []string{"conformance.Schema needs the application's users migrations and Open"},
	}
}
