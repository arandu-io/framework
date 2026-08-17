// Package config holds the typed application configuration.
//
// There is no config("app.name") lookup: a wrong key is a compile error, not a
// runtime panic on the first request that happens to need it.
//
// Load is the one entry point. It reads a .env from the working directory when
// there is one -- filling only what the environment has not already defined --
// and then validates the result. There is no second loader and no flag that
// moves the file.
//
// This package is a bridge. It is removed in v1.0.0; import
// github.com/arandu-io/framework/foundation/bootstrap directly.
//
// What replaces it is not a rename. Config was one struct of eleven fields that
// one function read from end to end, and what answers now is one struct per
// component, filled by bootstrap.LoadConfiguration from the same environment
// and the same .env:
//
//	cfg, err := bootstrap.LoadConfiguration()
//	app := foundation.New(cfg)
//
// So this package holds an implementation rather than aliases, which is what
// makes it unlike the other bridges in the collection. Nothing in the framework
// reads it: the boot path takes Configuration, and Load is here for an
// application that has not moved yet.
//
// The death date above is what keeps that from being permanent. Two ways to
// answer "what is this application configured with" is one of them going stale,
// and a setting added to one and not the other is a setting that silently does
// nothing.
//
// Two settings have no field on the other side yet, and they are why an
// application may still be reading this one: RedisURL and CSRFTTL.
package config
