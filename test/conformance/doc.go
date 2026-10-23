// Package conformance is the set of authentication suites an Arandu
// application runs against itself, each in one call from its
// tests/Conformance.
//
// Every application used to carry copies of these suites, and the copies
// drifted: some lagged a migration behind, some proved a shape the framework
// had since changed, some tested something the others did not. A kit is
// written once, here, and what is the application's -- its root, its users
// migrations, its user model, its sign-in screens -- is what it is handed:
//
//	func TestBoundaries(t *testing.T) { conformance.Boundaries(t, "../..") }
//	func TestSchema(t *testing.T)     { conformance.Schema(t, tests.AuthSchema()) }
//	func TestAccount(t *testing.T)    { conformance.Account(t, tests.AccountFixture) }
//	func TestModel(t *testing.T)      { conformance.ModelPersistence(t, tests.ModelFixture) }
//	func TestSessions(t *testing.T)   { conformance.SessionMatrix(t, tests.SessionFixture()) }
//	func TestRedaction(t *testing.T)  { conformance.Redaction(t, tests.RedactedRequests()...) }
//
// # The kits
//
//   - [Boundaries] reads the application's source: the community auth module
//     is not imported, the auth controller placeholder is package authui, no
//     code names the legacy SessionGuard, and the account rules that
//     hesape/auth/account owns are not copied back into app/.
//   - [Schema] migrates fresh databases with the application's users
//     migrations and the second factor's: the catalog holds them in order, the
//     schema is the portable one, an address is unique inside a tenant only, a
//     second-factor instant keeps the microsecond, and an existing schema
//     upgrades once and rolls back newest first.
//   - [Account] is hesape/auth/account/accounttest against the application's
//     users table, user model and user policy.
//   - [ModelPersistence] is the application's half of those rules: the model
//     the rules hand back, its roles column, its session subject and its JSON.
//   - [SessionMatrix] drives the application's sign-in screens through every
//     password and pending-second-factor outcome and counts the sessions they
//     write, with the subject resolver installed.
//   - [Redaction] holds the application's request types to what a log line and
//     a JSON rendering may carry.
//
// # What each kit is handed
//
// The application's boot, handler and fixtures are parameters, so a kit works
// with whatever the application already builds its tests from, and with the
// server and session helpers of framework/test when it uses them. A kit
// never opens a database itself: the engine is the application's, which is
// what lets the same kit run on SQLite, Postgres and MySQL.
package conformance
