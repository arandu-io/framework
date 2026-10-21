package unit

// historicalBridgeSurface is the closed list of declarations kept only for
// import-path compatibility until the bridge packages are removed in v1.0.0.
var historicalBridgeSurface = bridgeSurface{
	// Collected carries no method here: it is a Go alias for the hesape type,
	// so Publish and Names are declared there and reached through this name.
	// The two entries this list used to hold for them went when the struct did.
	"arandutest": inventoryItems(`
		func.ActingAs func.DrainOutbox func.NewClient func.Subject method.Client.Get
		method.Client.Post method.Response.Body
		method.Response.DontSee method.Response.Header method.Response.OK
		method.Response.RedirectsTo method.Response.See method.Response.Status type.Client
		type.Collected type.Response
	`),
	"config": inventoryItems(`
		const.EnvDev const.EnvProd const.EnvStaging func.Load func.ParseDatabaseURL
		method.Config.IsDev method.Config.Validate method.DatabaseConfig.DSN
		method.DatabaseConfig.Redacted method.DatabaseConfig.SQLitePath
		method.DatabaseConfig.Validate type.Config type.DatabaseConfig type.Env
	`),
	"data": inventoryItems(`
		const.DialectMySQL const.DialectPostgres const.DialectSQLite const.KeyText func.Day
		func.InTransaction func.NewID func.ParseDialect func.Tenant func.Transaction func.Wrap
		type.DB type.Dialect type.Migration type.Query type.Repository type.Tx
	`),
	"events": inventoryItems(`
		func.NewModule func.NewOutbox func.NewRelay func.WithRelay method.Module.Close
		method.Module.Diagnose method.Module.Health method.Module.Migrations method.Module.Name
		method.Module.Routes method.Module.Start method.Relay.Drain method.Relay.Lag
		method.Relay.Parked method.Relay.Run type.Event type.Locker type.Module type.Outbox
		type.Publisher type.PublisherFunc type.Recorder type.Relay type.RelayOptions type.Stored
		var.ErrNoTransaction
	`),
	// Router and Routes are Go aliases of hesape/routing/controllers, so their
	// methods are declared there and reached through these names; the
	// twenty-six entries this list held for them went when the envelopes did.
	"http": inventoryItems(`
		func.Back func.Chain func.NewRouter func.Redirect func.Refuse func.Reject func.StateFrom
		func.WithState type.Context type.Creator type.Destroyer type.Editor type.Indexer
		type.Invoker type.Middleware type.Renderer type.Route type.Router type.Routes type.Shower
		type.State type.Storer type.Updater
	`),
	"jobs": inventoryItems(`
		const.DefaultQueue func.Authorized func.ExponentialBackoff func.GrantFor func.New
		func.NewModule func.NewWorker method.HandlerFunc.Handle method.Job.Decode
		method.Module.Diagnose method.Module.Health method.Module.Migrations method.Module.Name
		method.Module.Routes method.Worker.Handle method.Worker.HandleFunc method.Worker.Names
		method.Worker.Run type.Handler type.HandlerFunc type.Job type.Module type.Queue
		type.Worker type.WorkerOptions var.ErrForged var.ErrNoName var.ErrNoTenant
	`),
	"kernel": inventoryItems(`
		const.Global const.PerTenant func.FormatRoutes func.New func.NewCacheModule func.NewLocker
		type.Background type.Bootable type.CacheConnection type.CacheModule type.Closable
		type.Diagnostic type.Health type.Kernel type.Locker type.Migratable type.Migration
		type.Module type.ReloadTagger type.RendererProvider type.Schedulable type.Scope type.Task
	`),
	"mail": inventoryItems(`
		func.New func.Render method.Address.String method.Address.Valid method.Array.Last
		method.Array.Name method.Array.Reset method.Array.Send method.Array.Sent method.Log.Name
		method.Log.Send method.Mailer.To method.Mailer.ToAddress method.Mailer.Transport
		method.Pending.BCC method.Pending.CC method.Pending.Send method.Resend.Name
		method.Resend.Send method.SMTP.Name method.SMTP.Send method.SendGrid.Name
		method.SendGrid.Send type.Address type.Array type.Content type.Envelope type.ErrRetryable
		type.Log type.Mailable type.Mailer type.Message type.Pending type.Renderer type.Resend
		type.SMTP type.SendGrid type.Transport var.ErrNoRecipient
	`),
	"observability": inventoryItems(`
		const.ConsolePath const.DefaultRecorderSize const.TracingHeader func.Client func.Dump
		func.DumpDie func.EditorLink func.FromContext func.IsDumpDie func.Log func.NewCollector
		func.NewConsole func.NewGauges func.NewLogger func.NewRecorder func.RootLogger
		func.Transport func.WithCollector func.WithCollectorSlot func.WithLogger type.Collector
		type.Console type.DumpRecord type.EventRecord type.ExternalRecord type.Frame
		type.GaugeName type.Gauges type.QueryRecord type.Recorded type.Recorder type.RenderRecord
		type.Timeline
	`),
	"observability/errorpage": inventoryItems(`
		func.Capture func.EditorLink func.Render func.RenderDump type.Options type.StackFrame
	`),
	"scheduler": inventoryItems(`
		func.MustParse func.New func.NewModule func.Parse method.Module.Boot method.Module.Close
		method.Module.Diagnose method.Module.Name method.Module.Routes method.Module.Scheduler
		method.Module.Start method.Schedule.Matches method.Schedule.Next method.Schedule.String
		method.Scheduler.Diagnose method.Scheduler.List method.Scheduler.RunNow
		method.Scheduler.Start method.Scheduler.Stop method.Scheduler.Tick type.Module
		type.Options type.Registered type.Schedule type.Scheduler type.Tenants
	`),
	// SessionStore, SessionBackend and MemoryBackend are Go aliases of
	// hesape/http, so their methods are declared there and reached through
	// these names.
	"security": inventoryItems(`
		const.FlashCookieName const.FlashLifetime const.IntendedCookieName const.IntendedLifetime
		const.MaxFlashBytes const.MaxSignInFailures const.MaxSignInFailuresPerClient
		const.MinPasswordLen const.PasswordConfirmationWindow const.RememberLifetime
		const.SessionCookieName const.SignInWindow func.Authorize func.Guest func.HashPassword
		func.LocalPath func.NeedsRehash func.NewCSRF func.NewFlash func.NewMemoryBackend
		func.NewMemoryThrottle func.NewSessionBackend func.NewSessionStore func.NewSigner
		func.PasswordConfirmedWithin func.Remember func.SystemGrant func.Tenant func.ValidTenant
		func.VerifyPassword method.MemoryThrottle.Attempt
		method.MemoryThrottle.Clear method.MemoryThrottle.Len method.MemoryThrottle.Refund
		type.Action type.CSRF type.Flash type.Grant
		type.MemoryBackend type.MemoryThrottle type.Policy type.SessionBackend type.SessionOption
		type.SessionStore type.SignInThrottle type.Signer type.Subject var.ErrCSRF
		var.ErrConfirmationNotStored var.ErrExpired var.ErrForbidden var.ErrInvalidPassword
		var.ErrNoSession var.ErrSessionExpired var.ErrSignature
	`),
	"storage": inventoryItems(`
		func.CleanKey func.Path type.File type.Store var.ErrBadKey var.ErrNoTenant var.ErrNotFound
	`),
	"validation": inventoryItems(`
		func.Confirmed func.Email func.Humanize func.MaxLen func.MinLen func.NotZero func.Required
		func.WithMessages type.CompileError type.CompileErrors type.Errors type.Input
		type.Messages type.Option type.Rules type.Set type.Validatable var.Compile var.MustCompile
	`),
	"view": inventoryItems(`
		const.AssetPath const.Stylesheet func.AssetHash func.Assets func.CSRF func.Handler
		func.Include func.New func.NewModule func.NewRenderer func.Register func.RegisterAsset
		func.RegisterLayout func.RegisterStylesheet func.Registered func.ReloadTag func.RenderInto
		func.Text func.TextAttr func.TextCSS func.TextJS func.TextURL func.URL func.UnsafeText
		func.Version func.WrongData func.Yield method.Module.Name method.Module.ReloadTag
		method.Module.Renderer method.Module.Routes method.Page.AdminLink method.Page.BrandName
		method.Page.CSRFToken method.Page.CanonicalURL method.Page.ErrorSummary
		method.Page.FieldError method.Page.FieldErrors method.Page.HasErrors method.Page.HomeLink
		method.Page.Locale method.Page.Translator method.Page.WithTranslator
		method.Page.IsCurrent method.Page.LogValue method.Page.LoginLink method.Page.LogoutLink
		method.Page.MarshalJSON method.Page.OldOr method.Page.OldValue method.Page.PageDescription
		method.Page.PageTitle method.Page.PanelLink method.Page.RegisterLink method.Page.SignedIn
		method.Page.SignedInName method.Page.WithToken type.Asset type.Func type.Layout
		type.LayoutFunc type.Module type.Page type.Renderer
	`),
}

// allowedHesapeImports is the closed list of direct dependency edges in the
// framework-to-Hesape boundary. Internal standard-library imports are not part
// of this compatibility inventory.
var allowedHesapeImports = bridgeSurface{
	"arandutest": inventoryItems(`
		github.com/arandu-io/hesape/arandutest github.com/arandu-io/hesape/auth
	`),
	"config": inventoryItems(`
		github.com/arandu-io/hesape/config
	`),
	"data": inventoryItems(`
		github.com/arandu-io/hesape/auth github.com/arandu-io/hesape/database
		github.com/arandu-io/hesape/database/migrations
	`),
	// The outbox migrations are hesape/events.Module's, handed over by
	// Module.Migrations, so this package names neither database/migrations nor
	// database/schema: it no longer writes a migration of its own.
	"events": inventoryItems(`
		github.com/arandu-io/hesape/events
	`),
	// http is here because the Application puts the configured name on every
	// request context with http.WithAppName, the key view.New reads. The http
	// bridge is frozen and does not re-export it.
	//
	// The rest is what the console kernel, the drivers and Configure compose:
	// every built-in command is hesape's, built here over the drivers, and the
	// drivers are hesape's stores, queue, session, mail and disks built from the
	// typed settings. Composing them is this package's job; implementing any of
	// them is not.
	//
	// translation is here because the Application puts the configured
	// catalogue on every request with translation.WithTranslator, beside the
	// name, where view.New reads it.
	//
	// queue/middleware is here because the worker queue:work starts runs
	// ReportToBatch, so a batch finishes without its handlers reporting.
	"foundation": inventoryItems(`
		github.com/arandu-io/hesape/auth github.com/arandu-io/hesape/bus
		github.com/arandu-io/hesape/cache github.com/arandu-io/hesape/cache/console
		github.com/arandu-io/hesape/config github.com/arandu-io/hesape/console
		github.com/arandu-io/hesape/console/scheduling
		github.com/arandu-io/hesape/database github.com/arandu-io/hesape/database/console
		github.com/arandu-io/hesape/database/console/migrations
		github.com/arandu-io/hesape/database/console/seeds
		github.com/arandu-io/hesape/database/migrations
		github.com/arandu-io/hesape/database/schema github.com/arandu-io/hesape/exception
		github.com/arandu-io/hesape/filesystem github.com/arandu-io/hesape/filesystem/s3
		github.com/arandu-io/hesape/foundation github.com/arandu-io/hesape/http
		github.com/arandu-io/hesape/http/middleware github.com/arandu-io/hesape/log
		github.com/arandu-io/hesape/publish/console github.com/arandu-io/hesape/queue
		github.com/arandu-io/hesape/queue/console github.com/arandu-io/hesape/queue/failed
		github.com/arandu-io/hesape/queue/middleware
		github.com/arandu-io/hesape/routing github.com/arandu-io/hesape/routing/middleware
		github.com/arandu-io/hesape/session github.com/arandu-io/hesape/translation
		github.com/arandu-io/hesape/view
	`),
	"foundation/bootstrap": inventoryItems(`
		github.com/arandu-io/hesape/cache github.com/arandu-io/hesape/config
		github.com/arandu-io/hesape/database github.com/arandu-io/hesape/exception
		github.com/arandu-io/hesape/filesystem github.com/arandu-io/hesape/log
		github.com/arandu-io/hesape/queue
		github.com/arandu-io/hesape/view
	`),
	// geo is a route-only envelope: the native package owns every handler and
	// this package translates only the router type until the envelope is gone.
	"geo": inventoryItems(`
		github.com/arandu-io/hesape/geo
	`),
	// routing/controllers is here because Router, NewRouter, Routes and the
	// controller interfaces are its names under the old ones. The adapter that
	// answers an action's error is hesape/http.NewAdapter, behind that router.
	"http": inventoryItems(`
		github.com/arandu-io/hesape/http github.com/arandu-io/hesape/pipeline
		github.com/arandu-io/hesape/routing github.com/arandu-io/hesape/routing/controllers
		github.com/arandu-io/hesape/validation
	`),
	// exception is here because Recover installs hesape/exception.Recover
	// rather than writing the panic path a second time, and http/middleware
	// because CSRFProtect, the session and token guards, Idempotent and
	// KeyBySession are its names under the old ones. routing/middleware
	// answers KeyByIP, and foundation answers Observe.
	"http/middleware": inventoryItems(`
		github.com/arandu-io/hesape/exception github.com/arandu-io/hesape/foundation
		github.com/arandu-io/hesape/http/middleware github.com/arandu-io/hesape/routing/middleware
	`),
	"jobs": inventoryItems(`
		github.com/arandu-io/hesape/queue github.com/arandu-io/hesape/queue/jobs
	`),
	"kernel": inventoryItems(`
		github.com/arandu-io/hesape/cache
	`),
	"mail": inventoryItems(`
		github.com/arandu-io/hesape/mail github.com/arandu-io/hesape/mail/transport
	`),
	// http is here because the sign-in screen draws the CSRF token CSRFProtect
	// put on the request context, read with http.CSRFTokenFrom.
	"modules/auth": inventoryItems(`
		github.com/arandu-io/hesape/database/migrations
		github.com/arandu-io/hesape/database/schema
		github.com/arandu-io/hesape/http
	`),
	"observability": inventoryItems(`
		github.com/arandu-io/hesape/log
	`),
	"observability/errorpage": inventoryItems(`
		github.com/arandu-io/hesape/exception github.com/arandu-io/hesape/log
	`),
	"scheduler": inventoryItems(`
		github.com/arandu-io/hesape/console/events github.com/arandu-io/hesape/console/scheduling
	`),
	"security": inventoryItems(`
		github.com/arandu-io/hesape/auth github.com/arandu-io/hesape/encryption
		github.com/arandu-io/hesape/hashing github.com/arandu-io/hesape/http
		github.com/arandu-io/hesape/session
	`),
	"storage": inventoryItems(`
		github.com/arandu-io/hesape/filesystem
	`),
	"validation": inventoryItems(`
		github.com/arandu-io/hesape/str github.com/arandu-io/hesape/validation
	`),
	// translation is here because view.Page carries the translator and the
	// locale hesape's Page carries, field for field, and answers the same
	// Translator and Locale a component asks a page for.
	"view": inventoryItems(`
		github.com/arandu-io/hesape/translation github.com/arandu-io/hesape/view
	`),
	// webhook is a route-only envelope like geo: the native package owns every
	// action, table and task, and this package registers the actions on the
	// router type until the envelope is gone. It declares no alias.
	"webhook": inventoryItems(`
		github.com/arandu-io/hesape/webhook
	`),
}
