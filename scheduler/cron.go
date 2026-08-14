// The cron expression, answered by
// github.com/arandu-io/hesape/console/scheduling.
//
// Nothing is parsed here. The five fields, the two shorthands, the Vixie rule
// that ORs day-of-month with day-of-week, and the bounded walk to the next run
// are all CronExpression's.

package scheduler

import (
	"time"

	"github.com/arandu-io/hesape/console/scheduling"
)

// Schedule is a parsed five-field cron expression.
//
// Five fields, not six: no seconds. A framework that offers second-level cron
// offers a way to write a busy loop by accident, and work that has to happen
// every few seconds is a worker, not a schedule.
//
// It is an envelope over hesape/console/scheduling.CronExpression rather than an
// alias, because the design diverged twice at once: the type was renamed, and
// two of its three methods were renamed with it (Matches became IsDue, Next
// became GetNextRunDate). Go cannot declare a method on another package's type,
// so an alias would drop both of the names the framework is written against.
type Schedule struct {
	inner scheduling.CronExpression
}

// String returns the expression it was parsed from, for `aru schedule:list`.
func (s Schedule) String() string { return s.inner.String() }

// Matches reports whether the schedule fires in the minute of t.
//
// It reaches CronExpression.IsDue.
func (s Schedule) Matches(t time.Time) bool { return s.inner.IsDue(t) }

// Next returns the first minute after t that matches, or the zero time when the
// expression matches nothing in a year -- February 30th, for instance.
//
// It reaches CronExpression.GetNextRunDate.
func (s Schedule) Next(t time.Time) time.Time { return s.inner.GetNextRunDate(t) }

// Parse reads a cron expression.
func Parse(spec string) (Schedule, error) {
	inner, err := scheduling.ParseCronExpression(spec)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{inner: inner}, nil
}

// MustParse is Parse for a constant. It panics, which is right for a schedule
// written in source: a module with an unparseable spec must not boot.
//
// The panic keeps this package's prefix rather than taking hesape's, because it
// names the package the caller wrote against.
func MustParse(spec string) Schedule {
	s, err := Parse(spec)
	if err != nil {
		panic("scheduler: " + err.Error())
	}
	return s
}
