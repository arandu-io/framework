package conformance

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth/account"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// SchemaFixture is what Schema is handed: the application's users migrations
// and a way to open an empty database.
type SchemaFixture struct {
	// Users are the application's migrations of the users table, oldest
	// first: the one that creates it and the ones that add to it. The second
	// factor's migrations are hesape/auth/account's, and Schema adds them.
	Users []migrations.Migration
	// Open returns a connection to a fresh, empty database. It is called once
	// per subtest; the application closes what it opened in t.Cleanup.
	Open func(t *testing.T) *database.Connection
	// Catalog is the path the application registers its production
	// migrations under. Empty is migrations.DefaultPath.
	Catalog string
}

// expectedColumn is one column of a table as Schema expects it: its name, its family
// of type -- "varchar", "text", "integer" or "time", which every engine reads
// back under its own spelling -- and whether it takes NULL.
type expectedColumn struct {
	Name     string
	Family   string
	Nullable bool
}

// usersColumns are the columns of the users table the account rules read and
// write. The table may have more -- the roles, a document number -- and they
// are the application's.
var usersColumns = []expectedColumn{
	{Name: "id", Family: "string"},
	{Name: "tenant_id", Family: "string"},
	{Name: "email", Family: "string"},
	{Name: "password", Family: "string"},
	// Whether a name is required is the application's to decide, so it is
	// not held to either answer.
	{Name: "name", Family: "string"},
	{Name: "verified_at", Family: "time", Nullable: true},
	{Name: "created_at", Family: "time"},
}

// The second factor's tables are hesape/auth/account's, so their columns are
// exactly these.
var (
	twoFactorColumns = []expectedColumn{
		{Name: "user_id", Family: "varchar"},
		{Name: "tenant_id", Family: "varchar"},
		{Name: "secret", Family: "text"},
		{Name: "confirmed_at", Family: "time", Nullable: true},
		{Name: "last_used_step", Family: "integer"},
		{Name: "created_at", Family: "time"},
	}
	recoveryCodeColumns = []expectedColumn{
		{Name: "id", Family: "varchar"},
		{Name: "tenant_id", Family: "varchar"},
		{Name: "user_id", Family: "varchar"},
		{Name: "code_hash", Family: "text"},
		{Name: "used_at", Family: "time", Nullable: true},
		{Name: "created_at", Family: "time"},
	}
)

// schemaRuns numbers the private migration path each Schema call registers
// its migrations under, so two calls in one test binary never share one.
var schemaRuns atomic.Int64

// Schema migrates fresh databases with the application's users migrations and
// hesape/auth/account's, and checks what an application upgraded from its own
// copy of the account rules relies on:
//
//   - the production catalog holds those migrations in that order beside any
//     other the application adds, and each can be rolled back;
//   - a fresh database gets the users columns the account rules read, and the
//     second factor's two tables exactly, with the recovery codes' owner index;
//   - the same address is accepted in two tenants and refused twice in one;
//   - a second-factor instant keeps the microsecond, in a fresh database and
//     in tables an application's own copy created to the second, which the
//     widen-two-factor-time-precision migration widens on engines with a time
//     precision;
//   - a database holding only the users table upgrades by the second factor's
//     migrations alone, runs nothing on a replay, and rolls back that batch
//     newest first, leaving the users table.
func Schema(t *testing.T, f SchemaFixture) {
	t.Helper()
	if len(f.Users) == 0 || f.Open == nil {
		t.Fatal("conformance.Schema needs the application's users migrations and Open")
	}
	catalog := f.Catalog
	if catalog == "" {
		catalog = migrations.DefaultPath
	}
	all := append(slices.Clone(f.Users), account.Migrations()...)
	names := migrationNames(all)
	userNames := names[:len(f.Users)]
	factorNames := names[len(f.Users):]
	path := fmt.Sprintf("framework/test/conformance/auth-schema/%d", schemaRuns.Add(1))
	for _, migration := range all {
		migrations.Register(migration, path)
	}

	t.Run("TheCatalogHoldsTheNativeAuthMigrationsInOrder", func(t *testing.T) {
		for _, problem := range catalogProblems(migrations.Registered(catalog), names) {
			t.Error(problem)
		}
	})

	t.Run("AFreshDatabaseGetsThePortableSchema", func(t *testing.T) {
		h := newSchemaHarness(t, f.Open(t))
		applied, err := h.migrator.Run(h.ctx, []string{path}, migrations.Options{})
		if err != nil {
			t.Fatalf("migrating a fresh database: %v", err)
		}
		if !slices.Equal(applied, names) {
			t.Fatalf("a fresh database applied %v, want %v", applied, names)
		}
		for _, problem := range columnProblems("users", h.columns(t, "users"), usersColumns, false) {
			t.Error(problem)
		}
		for _, problem := range columnProblems("user_two_factor", h.columns(t, "user_two_factor"), twoFactorColumns, true) {
			t.Error(problem)
		}
		for _, problem := range columnProblems("user_recovery_codes", h.columns(t, "user_recovery_codes"), recoveryCodeColumns, true) {
			t.Error(problem)
		}
		for table, key := range map[string]string{"users": "id", "user_two_factor": "user_id", "user_recovery_codes": "id"} {
			if got := h.primaryKey(t, table); !slices.Equal(got, []string{key}) {
				t.Errorf("%s has primary key %v, want [%s]", table, got, key)
			}
		}
		if got := h.indexColumns(t, "user_recovery_codes", "user_recovery_codes_owner_idx"); !slices.Equal(got, []string{"tenant_id", "user_id"}) {
			t.Errorf("user_recovery_codes_owner_idx covers %v, want [tenant_id user_id]", got)
		}
	})

	t.Run("AnAddressIsUniqueInsideATenantOnly", func(t *testing.T) {
		h := newSchemaHarness(t, f.Open(t))
		if _, err := h.migrator.Run(h.ctx, []string{path}, migrations.Options{}); err != nil {
			t.Fatalf("migrating: %v", err)
		}
		if err := h.insertUser(t, "user-a", "tenant-a", "same@example.com"); err != nil {
			t.Fatalf("inserting the first tenant's account: %v", err)
		}
		if err := h.insertUser(t, "user-b", "tenant-b", "same@example.com"); err != nil {
			t.Fatalf("the same address in another tenant was refused: %v", err)
		}
		if err := h.insertUser(t, "user-c", "tenant-a", "same@example.com"); err == nil {
			t.Fatal("the same address was accepted twice inside one tenant")
		}
	})

	t.Run("ASecondFactorInstantKeepsTheMicrosecond", func(t *testing.T) {
		h := newSchemaHarness(t, f.Open(t))
		if _, err := h.migrator.Run(h.ctx, []string{path}, migrations.Options{}); err != nil {
			t.Fatalf("migrating: %v", err)
		}
		h.assertMicrosecond(t)
	})

	t.Run("TablesCreatedToTheSecondAreWidenedToTheMicrosecond", func(t *testing.T) {
		h := newSchemaHarness(t, f.Open(t))
		if err := h.migrator.RunPending(h.ctx, f.Users, migrations.Options{}); err != nil {
			t.Fatalf("installing the users table: %v", err)
		}
		// The second factor's tables as an application's own copy of the
		// account rules created them, before instants kept the microsecond,
		// recorded under the name the package's migration carries.
		h.createSecondPrecisionTables(t)
		if err := h.repository.Log(h.ctx, factorNames[0], 1); err != nil {
			t.Fatalf("recording the copy's migration: %v", err)
		}
		applied, err := h.migrator.Run(h.ctx, []string{path}, migrations.Options{})
		if err != nil {
			t.Fatalf("upgrading: %v", err)
		}
		if !slices.Equal(applied, factorNames[1:]) {
			t.Fatalf("the upgrade applied %v, want %v", applied, factorNames[1:])
		}
		h.assertMicrosecond(t)
	})

	t.Run("AnExistingSchemaUpgradesOnceAndRollsBackNewestFirst", func(t *testing.T) {
		h := newSchemaHarness(t, f.Open(t))
		if err := h.migrator.RunPending(h.ctx, f.Users, migrations.Options{}); err != nil {
			t.Fatalf("installing the users table alone: %v", err)
		}
		if h.hasTable(t, "user_two_factor") {
			t.Fatal("the users migrations created the second factor's table: it is hesape/auth/account's")
		}
		applied, err := h.migrator.Run(h.ctx, []string{path}, migrations.Options{})
		if err != nil {
			t.Fatalf("upgrading: %v", err)
		}
		if !slices.Equal(applied, factorNames) {
			t.Fatalf("the upgrade applied %v, want only %v", applied, factorNames)
		}
		replayed, err := h.migrator.Run(h.ctx, []string{path}, migrations.Options{})
		if err != nil {
			t.Fatalf("replaying: %v", err)
		}
		if len(replayed) != 0 {
			t.Fatalf("a replay applied %v again", replayed)
		}
		reverted, err := h.migrator.Rollback(h.ctx, []string{path}, migrations.Options{})
		if err != nil {
			t.Fatalf("rolling the upgrade back: %v", err)
		}
		newestFirst := slices.Clone(factorNames)
		slices.Reverse(newestFirst)
		if !slices.Equal(reverted, newestFirst) {
			t.Fatalf("the rollback reverted %v, want %v: a batch is rolled back newest first", reverted, newestFirst)
		}
		if !h.hasTable(t, "users") {
			t.Fatalf("rolling the second factor back removed the users table %v created", userNames)
		}
		if h.hasTable(t, "user_two_factor") || h.hasTable(t, "user_recovery_codes") {
			t.Fatal("rolling the second factor back left its tables behind")
		}
	})
}

// migrationNames are the names of the migrations, in order.
func migrationNames(list []migrations.Migration) []string {
	names := make([]string, len(list))
	for i, m := range list {
		names[i] = m.GetName()
	}
	return names
}

// catalogProblems checks that the registered catalog holds the wanted
// migrations, in the wanted order, among whatever else it holds, and that each
// of them can be rolled back.
func catalogProblems(registered []migrations.Migration, wanted []string) []string {
	want := make(map[string]bool, len(wanted))
	for _, name := range wanted {
		want[name] = true
	}
	var found []string
	var problems []string
	for _, m := range registered {
		if !want[m.GetName()] {
			continue
		}
		found = append(found, m.GetName())
		if _, ok := m.(migrations.ReversibleMigration); !ok {
			problems = append(problems, fmt.Sprintf("the native auth migration %s cannot be rolled back", m.GetName()))
		}
	}
	if !slices.Equal(found, wanted) {
		problems = append(problems, fmt.Sprintf("the production catalog holds the native auth migrations %v, want %v in that order", found, wanted))
	}
	return problems
}

// columnProblems compares what a table has with what is expected of it.
// exact also refuses a column that is not expected, and an order other than
// the expected one.
func columnProblems(table string, got []schema.ColumnInfo, want []expectedColumn, exact bool) []string {
	var problems []string
	byName := make(map[string]schema.ColumnInfo, len(got))
	var order []string
	for _, c := range got {
		byName[c.Name] = c
		order = append(order, c.Name)
	}
	var wantOrder []string
	for _, w := range want {
		wantOrder = append(wantOrder, w.Name)
		c, ok := byName[w.Name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has no %s column", table, w.Name))
			continue
		}
		if family := typeFamily(c.TypeName, c.Type); !sameFamily(family, w.Family) {
			problems = append(problems, fmt.Sprintf("%s.%s is a %s column (%s), want %s", table, w.Name, family, c.TypeName, w.Family))
		}
		if c.Nullable != w.Nullable && (exact || w.Nullable) {
			problems = append(problems, fmt.Sprintf("%s.%s nullable = %v, want %v", table, w.Name, c.Nullable, w.Nullable))
		}
	}
	if exact && len(problems) == 0 && !reflect.DeepEqual(order, wantOrder) {
		problems = append(problems, fmt.Sprintf("%s has columns %v, want exactly %v", table, order, wantOrder))
	}
	return problems
}

// typeFamily reads a column type, as any engine spells it, into the family
// Schema compares.
func typeFamily(typeName, full string) string {
	name := strings.ToLower(typeName)
	if name == "" {
		name = strings.ToLower(full)
	}
	if i := strings.IndexAny(name, "( "); i > 0 && !strings.HasPrefix(name, "character varying") && !strings.HasPrefix(name, "timestamp") && !strings.HasPrefix(name, "double") {
		name = name[:i]
	}
	switch {
	case strings.HasPrefix(name, "varchar"), strings.HasPrefix(name, "character varying"), strings.HasPrefix(name, "nvarchar"),
		name == "char", name == "character", name == "bpchar":
		return "varchar"
	case strings.HasSuffix(name, "text"), name == "clob":
		return "text"
	case strings.HasPrefix(name, "timestamp"), strings.HasPrefix(name, "datetime"), name == "date", name == "time":
		return "time"
	case strings.Contains(name, "int"):
		return "integer"
	}
	return name
}

// sameFamily compares a column's family with the expected one; "string"
// accepts both varchar and text.
func sameFamily(got, want string) bool {
	if want == "string" {
		return got == "varchar" || got == "text"
	}
	return got == want
}

// asTime reads a stored instant back, as a driver hands it: a time.Time, or
// the text an engine without a time type stores.
func asTime(v any) (time.Time, error) {
	switch value := v.(type) {
	case time.Time:
		return value, nil
	case []byte:
		return parseTime(string(value))
	case string:
		return parseTime(value)
	}
	return time.Time{}, fmt.Errorf("the column holds a %T, not an instant", v)
}

func parseTime(text string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00", "2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00", time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999",
	} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("the column holds %q, which is not an instant", text)
}

// schemaHarness is one fresh database with a migrator over it.
type schemaHarness struct {
	ctx        context.Context
	db         *database.DB
	builder    *schema.Builder
	migrator   *migrations.Migrator
	repository *migrations.DatabaseMigrationRepository
}

func newSchemaHarness(t *testing.T, conn *database.Connection) *schemaHarness {
	t.Helper()
	if conn == nil {
		t.Fatal("conformance.Schema: Open returned no connection")
	}
	name := conn.GetName()
	if name == "" {
		name = "conformance"
	}
	dialect, err := database.ParseDialect(conn.GetDriverName())
	if err != nil {
		t.Fatalf("conformance.Schema: the connection names no dialect: %v", err)
	}
	connections := database.NewConnectionResolver(map[string]database.ConnectionInterface{name: conn})
	connections.SetDefaultConnection(name)
	resolver := database.MigrationResolver{Resolver: connections}
	repository := migrations.NewDatabaseMigrationRepository(resolver, "conformance_auth_migrations")
	migrator := migrations.NewMigrator(repository, resolver, nil)
	migrator.SetConnection(name)
	ctx := context.Background()
	if err := repository.CreateRepository(ctx); err != nil {
		t.Fatalf("creating the migration ledger: %v", err)
	}
	return &schemaHarness{
		ctx: ctx, db: database.Wrap(conn.GetRawPDO(), dialect),
		builder: database.ForMigrations(conn).Schema(), migrator: migrator, repository: repository,
	}
}

func (h *schemaHarness) columns(t *testing.T, table string) []schema.ColumnInfo {
	t.Helper()
	columns, err := h.builder.GetColumns(h.ctx, table)
	if err != nil {
		t.Fatalf("reading the columns of %s: %v", table, err)
	}
	return columns
}

func (h *schemaHarness) indexes(t *testing.T, table string) []schema.IndexInfo {
	t.Helper()
	indexes, err := h.builder.GetIndexes(h.ctx, table)
	if err != nil {
		t.Fatalf("reading the indexes of %s: %v", table, err)
	}
	return indexes
}

func (h *schemaHarness) primaryKey(t *testing.T, table string) []string {
	t.Helper()
	for _, index := range h.indexes(t, table) {
		if index.Primary {
			return index.Columns
		}
	}
	return nil
}

func (h *schemaHarness) indexColumns(t *testing.T, table, name string) []string {
	t.Helper()
	for _, index := range h.indexes(t, table) {
		if index.Name == name {
			return index.Columns
		}
	}
	return nil
}

func (h *schemaHarness) hasTable(t *testing.T, table string) bool {
	t.Helper()
	ok, err := h.builder.HasTable(h.ctx, table)
	if err != nil {
		t.Fatalf("checking for %s: %v", table, err)
	}
	return ok
}

// insertUser writes a users row with the columns the account rules own, and
// a value of its own in every other column that takes neither NULL nor a
// default -- the roles, a document number -- distinct per row, so a unique
// column of the application's never decides the test.
func (h *schemaHarness) insertUser(t *testing.T, id, tenant, email string) error {
	t.Helper()
	values := map[string]any{
		"id": id, "tenant_id": tenant, "email": email, "password": "password-hash",
		"created_at": time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC),
	}
	columns := []string{"id", "tenant_id", "email", "password", "created_at"}
	for _, c := range h.columns(t, "users") {
		if _, known := values[c.Name]; known || c.Nullable || c.Default != nil || c.AutoIncrement {
			continue
		}
		switch typeFamily(c.TypeName, c.Type) {
		case "time":
			values[c.Name] = time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
		case "integer":
			values[c.Name] = 0
		case "bool", "boolean", "tinyint":
			values[c.Name] = false
		default:
			if c.Name == "roles" {
				values[c.Name] = "[]"
			} else {
				values[c.Name] = c.Name + "-" + id
			}
		}
		columns = append(columns, c.Name)
	}
	args := make([]any, len(columns))
	for i, c := range columns {
		args[i] = values[c]
	}
	_, err := h.db.ExecContext(h.ctx,
		`INSERT INTO users (`+strings.Join(columns, ", ")+`) VALUES (`+strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")+`)`,
		args...)
	return err
}

// assertMicrosecond stores an enrolment whose instants carry microseconds and
// reads them back unrounded.
func (h *schemaHarness) assertMicrosecond(t *testing.T) {
	t.Helper()
	instant := time.Date(2026, 10, 19, 12, 34, 56, 123456000, time.UTC)
	if _, err := h.db.ExecContext(h.ctx,
		`INSERT INTO user_two_factor (user_id, tenant_id, secret, confirmed_at, last_used_step, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"user-a", "tenant-a", "sealed", instant, 0, instant); err != nil {
		t.Fatalf("storing an enrolment: %v", err)
	}
	for _, column := range []string{"confirmed_at", "created_at"} {
		var stored any
		if err := h.db.QueryRowContext(h.ctx, `SELECT `+column+` FROM user_two_factor WHERE user_id = ?`, "user-a").Scan(&stored); err != nil {
			t.Fatalf("reading %s: %v", column, err)
		}
		got, err := asTime(stored)
		if err != nil {
			t.Fatalf("reading %s: %v", column, err)
		}
		if !got.Equal(instant) {
			t.Errorf("user_two_factor.%s read back %s, want %s: the column rounds the microsecond", column, got.UTC().Format(time.RFC3339Nano), instant.Format(time.RFC3339Nano))
		}
	}
}

// createSecondPrecisionTables creates the second factor's two tables with the
// columns the package's migration gives them and instants kept to the second,
// which is how the applications' own copies created them. On an engine with
// no time precision the difference does not exist, and the widening has
// nothing to change.
func (h *schemaHarness) createSecondPrecisionTables(t *testing.T) {
	t.Helper()
	if err := h.builder.Create(h.ctx, "user_two_factor", func(table *schema.Blueprint) {
		table.String("user_id").Primary()
		table.String("tenant_id")
		table.Text("secret")
		table.Timestamp("confirmed_at", 0).Nullable()
		table.BigInteger("last_used_step")
		table.Timestamp("created_at", 0)
	}); err != nil {
		t.Fatalf("creating the copy's user_two_factor: %v", err)
	}
	if err := h.builder.Create(h.ctx, "user_recovery_codes", func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("user_id")
		table.Text("code_hash")
		table.Timestamp("used_at", 0).Nullable()
		table.Timestamp("created_at", 0)
		table.Index([]string{"tenant_id", "user_id"}, "user_recovery_codes_owner_idx")
	}); err != nil {
		t.Fatalf("creating the copy's user_recovery_codes: %v", err)
	}
}
