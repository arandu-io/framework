package foundation

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/arandu-io/hesape/bus"
	cconsole "github.com/arandu-io/hesape/cache/console"
	"github.com/arandu-io/hesape/console"
	"github.com/arandu-io/hesape/console/scheduling"
	"github.com/arandu-io/hesape/database"
	dbconsole "github.com/arandu-io/hesape/database/console"
	dbmigrations "github.com/arandu-io/hesape/database/console/migrations"
	"github.com/arandu-io/hesape/database/console/seeds"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
	"github.com/arandu-io/hesape/log"
	pconsole "github.com/arandu-io/hesape/publish/console"
	"github.com/arandu-io/hesape/queue"
	qconsole "github.com/arandu-io/hesape/queue/console"
	"github.com/arandu-io/hesape/queue/failed"
	qmiddleware "github.com/arandu-io/hesape/queue/middleware"
	"github.com/arandu-io/hesape/routing"
)

// The built-in commands are a closed set, and this file is the whole of it.
// There is no registry and nothing is discovered: a command missing from
// builtins does not exist, and the kernel refuses an application or a module
// that declares a name already here.

// framework is how the refusal of a repeated name names a built-in.
const framework = "the framework"

// migrationTable is where applied migration names are recorded.
//
// It is not the migrations component's default, and the value is the
// compatibility: every database an Arandu application has migrated already
// carries this name, and pointing a binary at an empty table on a fully
// migrated database applies every migration a second time.
const migrationTable = "arandu_migrations"

// modulePath is the group the registered modules' migrations are listed under,
// kept apart from the application's own so the two halves stay tellable apart.
const modulePath = "modules"

// commandAliases are the names that forward to a built-in under another name:
// work is what the CLI hands the binary for queue:work, and route:list is the
// name the routes table goes by elsewhere.
var commandAliases = map[string]string{
	"work":       "queue:work",
	"route:list": "routes",
}

// builtinParts is what the built-in commands are composed over. Without
// drivers -- an Application made by New rather than by Configure -- only the
// commands that need nothing but the Application are composed.
type builtinParts struct {
	drivers *Drivers
	tasks   *scheduling.TaskModule

	// seed runs the application's seeders: the named one when args[0] names
	// one, the root one otherwise.
	seed func(ctx context.Context, args []string) (string, error)
	// jobs registers the application's job handlers on a worker.
	jobs func(w *queue.Worker)
	// prunables answers what model:prune prunes.
	prunables map[string]func(ctx context.Context) (int, error)

	version, commit string
}

// builtins is every command the framework answers, in the order a listing
// would read them if it were not sorted.
func builtins(a *Application, p builtinParts) []declared {
	out := []declared{
		{by: framework, command: serveCommand(a)},
		{by: framework, boots: true, command: routesCommand(a)},
		{by: framework, command: versionCommand(a, p.version, p.commit)},
		{by: framework, command: pconsole.NewPublishCommand(".", a.Publications).Command()},
	}
	d := p.drivers
	if d == nil {
		return out
	}

	env := d.Settings.App.Env
	resolver := database.NewSingleConnectionResolver(d.DB, "")
	wipe := database.Wiper(resolver)
	queues := queueDeps(a, d, p.jobs)

	// The modules' migrations are put in the registry the migrator reads,
	// under their own group. A module is a value the Application holds, so it is
	// asked; the application's own announce themselves from init(). The queue's
	// two tables join them, asked of the same values its commands use.
	for _, migration := range append(a.Migrations(), queues.Migrations()...) {
		migrations.Register(migration, modulePath)
	}

	var seed func(ctx context.Context, name string, args []string) (string, error)
	var seedAfterMigrate func(ctx context.Context, name string) error
	if p.seed != nil {
		seed = func(ctx context.Context, name string, args []string) (string, error) {
			if name != "" {
				args = append([]string{name}, args...)
			}
			return p.seed(ctx, args)
		}
		seedAfterMigrate = func(ctx context.Context, name string) error {
			_, err := seed(ctx, name, nil)
			return err
		}
	}

	migrator := database.NewMigrator(d.DB, migrationTable)
	for _, c := range dbmigrations.Commands(dbmigrations.Deps{
		Migrator:      migrator,
		Creator:       migrations.NewMigrationCreator(""),
		MigrationPath: filepath.Join("database", "migrations"),
		// Nil is every group: the application's own register in the default
		// group, the modules' in theirs.
		MigrationGroups: nil,
		Seed:            seedAfterMigrate,
		Wipe:            wipe,
		// fresh, reset and refresh empty the whole schema and run only in dev.
		Environment: env,
	}) {
		out = append(out, declared{by: framework, command: c})
	}

	for _, c := range seeds.Commands(seeds.Deps{
		Seed:        seed,
		Environment: string(env),
		SeederPath:  filepath.Join("database", "seeders"),
	}) {
		out = append(out, declared{by: framework, command: c})
	}

	for _, c := range dbconsole.Commands(dbconsole.Deps{
		Connections: resolver,
		Wipe:        wipe,
		Tables:      tables(resolver),
		Prunables:   p.prunables,
		Environment: string(env),
	}) {
		out = append(out, declared{by: framework, command: c})
	}

	for _, c := range qconsole.Commands(queues) {
		// The worker reaches the services a request does, and they are wired at
		// boot; a worker that skipped it would be a second, subtly different
		// application.
		out = append(out, declared{by: framework, boots: c.Name == "queue:work", command: c})
	}

	if p.tasks != nil {
		for _, c := range scheduling.TaskCommands(p.tasks) {
			out = append(out, declared{by: framework, boots: true, command: c})
		}
	}

	out = append(out,
		declared{by: framework, command: cconsole.NewClearCommand(d.Cache).Command()},
		declared{by: framework, command: cconsole.NewForgetCommand(d.Cache).Command()},
		declared{by: framework, command: cconsole.NewPruneStaleTagsCommand(d.Cache).Command()},
	)
	return out
}

// serveCommand boots the application and serves it until SIGINT or SIGTERM.
// It boots inside its own Run, because Run shuts the application down itself.
func serveCommand(a *Application) console.Command {
	return console.Command{
		Name:        "serve",
		Description: "serve the application over HTTP until interrupted",
		Run: func(ctx context.Context, _ *console.IO) error {
			if err := a.Boot(ctx); err != nil {
				return err
			}
			return a.Run(ctx)
		},
	}
}

// routesCommand prints the route table, grouped by module.
func routesCommand(a *Application) console.Command {
	return console.Command{
		Name:        "routes",
		Description: "list the registered routes",
		Run: func(_ context.Context, o *console.IO) error {
			_, err := fmt.Fprint(o.GetOutput(), routing.FormatRoutes(a.Routes()))
			return err
		},
	}
}

// versionCommand prints the application's name, its version and the commit it
// was built from. Both are stamped by the build; unstamped, they read dev and
// unknown.
func versionCommand(a *Application, version, commit string) console.Command {
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "unknown"
	}
	return console.Command{
		Name:        "version",
		Description: "print the application's version and the commit it was built from",
		Run: func(_ context.Context, o *console.IO) error {
			o.Line("%s %s (%s)", a.cfg.App.Name, version, commit)
			return nil
		},
	}
}

// queueDeps are the queue's collaborators, built once: the provider and the
// repository each own a table, and the migrations are asked of these same
// values so the table a migration creates is the table a command reads.
//
// The one queue is registered under the value QUEUE_CONNECTION holds, which is
// also the name its driver answers to. The pause flag and the restart signal
// are written to the shared store; with none the manager is left without a
// cache on purpose and queue:pause answers that it cannot, rather than
// recording a pause no worker in another process would see.
func queueDeps(a *Application, d *Drivers, jobs func(*queue.Worker)) qconsole.Deps {
	s := d.Settings.Queue
	batches := bus.NewDatabaseBatchRepository(d.DB)
	manager := queue.NewQueueManager().Extend(string(s.Connection), d.Queue)
	if d.SharedCache != nil {
		manager = manager.SetCache(d.SharedCache)
	}
	return qconsole.Deps{
		Manager:  manager,
		Failures: failed.NewDatabaseFailedJobProvider(d.DB, ""),
		Batches:  batches,
		Worker: qconsole.WorkerDeps{
			Queue: d.Queue,
			Options: queue.WorkerOptions{
				Queue:       s.Default,
				Concurrency: s.Workers,
				Lease:       s.RetryAfter,
				MaxTries:    s.MaxAttempts,
			},
			Register: reportingToBatches(jobs, batches, d.Queue),
			// Asked when the worker starts, after the boot that built it: a
			// finished job lands on the debug console exactly like a request,
			// and only when something is recording.
			Recorder: func() *log.Recorder { return a.Recorder() },
		},
	}
}

// tables answers the tables of a connection for db:show and db:table, read
// through the schema builder -- the door every migration writes DDL through, so
// what is listed and what a migration created cannot describe different
// tables.
func tables(resolver database.ConnectionResolverInterface) func(context.Context, string) ([]dbconsole.TableInfo, error) {
	return func(ctx context.Context, connection string) ([]dbconsole.TableInfo, error) {
		conn, err := resolver.Connection(connection)
		if err != nil {
			return nil, err
		}
		concrete, ok := conn.(*database.Connection)
		if !ok {
			return nil, fmt.Errorf("listing the tables needs a *database.Connection, and %q resolved to %T", connection, conn)
		}
		found, err := schema.NewBuilder(database.ForSchema(concrete)).GetTables(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]dbconsole.TableInfo, 0, len(found))
		for _, table := range found {
			out = append(out, dbconsole.TableInfo{
				Schema: table.Schema,
				Name:   table.Name,
				// The catalogue carries no row count, and -1 prints as unknown:
				// counting would be a full scan per table on the cheap look.
				Rows: -1,
				Size: table.Size,
			})
		}
		return out, nil
	}
}

// applicationKernel is the kernel of an Application made by New and composed
// by hand: the commands that need nothing but the Application and its modules,
// with every Commandable module's commands beside them.
func (a *Application) applicationKernel() (*consoleKernel, error) {
	k, err := compose(append(builtins(a, builtinParts{}), a.moduleCommands()...), commandAliases)
	if err != nil {
		return nil, err
	}
	k.environment = a.cfg.App.Env
	return k, nil
}

// moduleCommands collects what every Commandable module declares, in
// registration order. They reach the module, which is wired at boot, so each
// one boots the application first.
func (a *Application) moduleCommands() []declared {
	var out []declared
	for _, m := range a.modules {
		c, ok := capable[Commandable](m)
		if !ok {
			continue
		}
		for _, command := range c.Commands() {
			out = append(out, declared{by: fmt.Sprintf("the module %q", m.Name()), boots: true, command: command})
		}
	}
	return out
}

// reportingToBatches is the registration queue:work runs: the handlers, then
// the middleware that reports every job to its batch and its chain, outermost,
// so a batch finishes and a chain moves on without its handlers calling
// bus.Handled. It reports to the repository the batch commands read and pushes
// what an outcome triggers on the queue the worker drains.
//
// Nil stays nil: with no handler there is no worker to report from, and
// queue:work refuses to start one.
func reportingToBatches(jobs func(*queue.Worker), batches bus.BatchRepository, q queue.Queue) func(*queue.Worker) {
	if jobs == nil {
		return nil
	}
	return func(w *queue.Worker) {
		jobs(w)
		o := w.Options()
		o.Middleware = append([]qmiddleware.Middleware{qmiddleware.NewReportToBatch(batches, queue.NewBusAdapter(q), w)}, o.Middleware...)
		w.SetOptions(o)
	}
}
