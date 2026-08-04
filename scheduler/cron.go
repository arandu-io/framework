package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed five-field cron expression.
//
// Five fields, not six: no seconds. A framework that offers second-level cron
// offers a way to write a busy loop by accident, and work that has to happen
// every few seconds is a worker, not a schedule.
//
// Parsed rather than pulled from a library, because the core has two
// dependencies (ADR 0004) and this is eighty lines. What it supports is the
// syntax people write: `*`, `5`, `1-5`, `*/15`, `1,15,30`, and the two names
// that read better than numbers (`@daily`, `@hourly`).
type Schedule struct {
	minute  fieldSet
	hour    fieldSet
	day     fieldSet
	month   fieldSet
	weekday fieldSet
	spec    string
}

// String returns the expression it was parsed from, for `aru schedule:list`.
func (s Schedule) String() string { return s.spec }

// fieldSet is which values of one field match. Fixed size because every cron
// field has a small known range, and a bitmap over a map is one allocation
// versus none.
type fieldSet [60]bool

// Parse reads a cron expression.
func Parse(spec string) (Schedule, error) {
	trimmed := strings.TrimSpace(spec)

	// The two shorthands worth having. @every and friends are not here: a
	// schedule that says "@every 90s" is the busy loop this format avoids.
	switch trimmed {
	case "@hourly":
		trimmed = "0 * * * *"
	case "@daily", "@midnight":
		trimmed = "0 0 * * *"
	case "@weekly":
		trimmed = "0 0 * * 0"
	case "@monthly":
		trimmed = "0 0 1 * *"
	}

	fields := strings.Fields(trimmed)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf(
			"a cron expression has five fields (minute hour day month weekday), and %q has %d",
			spec, len(fields))
	}

	s := Schedule{spec: strings.TrimSpace(spec)}
	for _, f := range []struct {
		name     string
		text     string
		min, max int
		into     *fieldSet
	}{
		{"minute", fields[0], 0, 59, &s.minute},
		{"hour", fields[1], 0, 23, &s.hour},
		{"day of month", fields[2], 1, 31, &s.day},
		{"month", fields[3], 1, 12, &s.month},
		{"day of week", fields[4], 0, 6, &s.weekday},
	} {
		if err := parseField(f.text, f.min, f.max, f.into); err != nil {
			return Schedule{}, fmt.Errorf("%s in %q: %w", f.name, spec, err)
		}
	}
	return s, nil
}

// MustParse is Parse for a constant. It panics, which is right for a schedule
// written in source: a module with an unparseable spec must not boot.
func MustParse(spec string) Schedule {
	s, err := Parse(spec)
	if err != nil {
		panic("scheduler: " + err.Error())
	}
	return s
}

func parseField(text string, min, max int, into *fieldSet) error {
	for _, part := range strings.Split(text, ",") {
		step := 1
		if base, stepText, hasStep := strings.Cut(part, "/"); hasStep {
			parsed, err := strconv.Atoi(stepText)
			if err != nil || parsed < 1 {
				return fmt.Errorf("%q is not a step", stepText)
			}
			step = parsed
			part = base
		}

		low, high := min, max
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			from, to, _ := strings.Cut(part, "-")
			var err error
			if low, err = value(from, min, max); err != nil {
				return err
			}
			if high, err = value(to, min, max); err != nil {
				return err
			}
			if low > high {
				return fmt.Errorf("%q counts backwards", part)
			}
		default:
			v, err := value(part, min, max)
			if err != nil {
				return err
			}
			low, high = v, v
		}

		for i := low; i <= high; i += step {
			into[i] = true
		}
	}
	return nil
}

func value(text string, min, max int) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", text)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%d is outside %d-%d", v, min, max)
	}
	return v, nil
}

// Matches reports whether the schedule fires in the minute of t.
//
// Day-of-month and day-of-week are OR when both are restricted, which is the
// behaviour of every cron since Vixie: "0 0 1 * 1" means the first of the month
// AND every Monday, not their intersection. It surprises people, and matching
// the surprise is better than being the one implementation that differs.
func (s Schedule) Matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}

	dayRestricted := !full(s.day, 1, 31)
	weekdayRestricted := !full(s.weekday, 0, 6)

	switch {
	case dayRestricted && weekdayRestricted:
		return s.day[t.Day()] || s.weekday[int(t.Weekday())]
	case dayRestricted:
		return s.day[t.Day()]
	case weekdayRestricted:
		return s.weekday[int(t.Weekday())]
	default:
		return true
	}
}

// Next returns the first minute at or after t that matches.
//
// It walks minute by minute, bounded to a year. A schedule that matches nothing
// in a year matches nothing at all -- February 30th, for instance -- and
// returning the zero time is what lets `aru schedule:list` say so instead of
// hanging.
func (s Schedule) Next(t time.Time) time.Time {
	at := t.Truncate(time.Minute)
	for range 366 * 24 * 60 {
		at = at.Add(time.Minute)
		if s.Matches(at) {
			return at
		}
	}
	return time.Time{}
}

func full(set fieldSet, min, max int) bool {
	for i := min; i <= max; i++ {
		if !set[i] {
			return false
		}
	}
	return true
}
