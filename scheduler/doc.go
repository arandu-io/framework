// Package scheduler runs the tasks modules declare.
//
// A scheduler built on a system cron exists because the runtime has no resident
// process: cron calls a command every minute and the command decides what to
// run. That is two artifacts and a dependency on the operating system.
//
// Go has a resident process. The scheduler is a goroutine in the same binary,
// which is also what keeps the deploy story true: one image, no crontab to
// configure, nothing to forget when a machine is replaced.
//
// What it does not do is retry. A task that fails is logged and diagnosed, and
// the next window runs it again; work that needs its own retry budget enqueues a
// job, and the queue owns the retry. Scheduler fires, queue persists.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/console/scheduling directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. The target is
// hesape/console/scheduling and not hesape/scheduler: that one is a doc-only
// refusal saying the scheduler folded into console/scheduling, because two
// schedulers is two ways to say one thing.
//
// The death date above is what keeps this from being a second way to import one
// type. What it holds is the framework's side of the contract and nothing else:
// the cron expression, the due selection, the per-tenant expansion and the Grant
// a task runs under are all hesape's.
//
// Nothing here aliases, and the reason is the same in every case: the two
// designs name the same things differently.
//
//	Schedule   hesape/console/scheduling.CronExpression renamed the type and
//	           two of its three methods, and Go cannot declare a method on
//	           another package's type
//	Scheduler  a schedule there is declared through Schedule.Call and carried
//	           as an Event; here it is collected as a kernel.Task, and the task
//	           carries a Timeout, a Recorder and a kernel.Locker that a hesape
//	           Event has no field for
//	Module     it consumes kernel.Task and answers the kernel module contract,
//	           which is the framework's and not hesape's
package scheduler
