// Package jobs is the queue contract: work that happens after the response.
//
// The contract lives in the core and the drivers do not, for the same reason as
// data.Repository: Push takes a security.Grant, and the tenant comes from it.
// Moving that into an optional package would make the guarantee optional, and an
// optional guarantee is not one.
//
// A driver that needs a client installed is a module of its own, because in Go
// there is no optional dependency: a contract that carried a Redis client would
// put it in every project's go.sum, its build and its vulnerability surface.
//
// Delivery is at-least-once. A handler that cannot run twice safely is a handler
// with a bug -- the process can die between doing the work and acknowledging it,
// and no queue anywhere solves that.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/queue directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. It answers to two hesape
// packages:
//
//	hesape/queue       the Worker, its options and the backoff
//	hesape/queue/jobs  the Job, its constructor, GrantFor and Authorized
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: the loop that drains a queue, the
// decision to park a job and the hash of a payload all run in hesape, and what
// is left here translates.
//
// This bridge is more envelope than alias, and the reason is one difference: in
// hesape a Job settles itself -- Release, Delete, Fail -- where here the Queue
// settled it, and a job is a *jobs.Job there where it is a value here. Four
// names diverged that way:
//
//	Job              UUID rather than ID, and a pointer with a driver behind it
//	Queue            eleven methods rather than eight, and all but one renamed
//	Handler          takes *jobs.Job rather than a Job
//	Worker           Daemon rather than Run, and WorkerOptions renamed two fields
//
// None of the four could alias, and none of the four may change shape here. A
// driver implements Queue by these method names and marshals a Job by these
// field names, both in a module of its own; `aru make:job` emits a Handler with
// this signature, and every handler in a project is written against it. An
// alias would compile in this module and break all of them in silence, which is
// the one failure a build of the framework cannot catch.
//
// What crosses to hesape is the adapter in worker.go, and it is the only thing
// that has to know both shapes.
package jobs
