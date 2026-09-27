// Package schedule parses report schedules and computes their occurrences.
//
// Expressions are standard five-field cron (minute, hour, day of month, month, day of week) or one
// of the descriptors @hourly, @daily, @weekly, @monthly and @yearly. robfig/cron parses them; the
// occurrences are computed here, walking the local wall clock of the report's time zone, so that
// daylight saving transitions behave as the spec says: a local time that does not exist runs at the
// first instant after the gap, and a local time that happens twice runs once, at its first
// occurrence.
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Errors returned by Parse. Their messages are stable validation codes.
var (
	ErrInvalidExpression = errors.New("validation.cron")
	ErrInvalidTimezone   = errors.New("validation.timezone")
	ErrNeverRuns         = errors.New("validation.cron_never")
)

// horizon bounds the search for the next occurrence. Eight years always contains a 29 February
// that falls on any given weekday combination the parser accepts.
const horizon = 8 * 366

const starBit = 1 << 63

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// descriptors maps the accepted descriptors to their five-field form (used for descriptions).
var descriptors = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Schedule is a parsed expression bound to a time zone.
type Schedule struct {
	expr string
	spec *cron.SpecSchedule
	loc  *time.Location
}

// Parse validates expr and timezone (an IANA name). Seconds, @every and inline time zones are
// refused, and so is an expression that never matches a real date (such as 30 February).
func Parse(expr, timezone string) (*Schedule, error) {
	expr = strings.Join(strings.Fields(expr), " ")
	loc, err := LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(expr)
	if expr == "" || strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(lower, "@every") {
		return nil, ErrInvalidExpression
	}
	if strings.HasPrefix(expr, "@") {
		if _, ok := descriptors[lower]; !ok {
			return nil, ErrInvalidExpression
		}
		expr = lower
	}
	parsed, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidExpression, err)
	}
	spec, ok := parsed.(*cron.SpecSchedule)
	if !ok {
		return nil, ErrInvalidExpression
	}
	s := &Schedule{expr: expr, spec: spec, loc: loc}
	if s.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		return nil, ErrNeverRuns
	}
	return s, nil
}

// LoadLocation loads an IANA time zone, refusing the empty name and "Local" (which would depend on
// the server's configuration).
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, ErrInvalidTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalidTimezone
	}
	return loc, nil
}

// Expression is the normalized expression.
func (s *Schedule) Expression() string { return s.expr }

// Location is the schedule's time zone.
func (s *Schedule) Location() *time.Location { return s.loc }

// Next returns the first occurrence strictly after t, in UTC, or the zero time when there is none
// within the search horizon.
func (s *Schedule) Next(t time.Time) time.Time {
	after := t.In(s.loc)
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
	for range horizon {
		if s.dayMatches(day) {
			for h := range 24 {
				if s.spec.Hour&(1<<uint(h)) == 0 {
					continue
				}
				for m := range 60 {
					if s.spec.Minute&(1<<uint(m)) == 0 {
						continue
					}
					at := resolve(day.Year(), day.Month(), day.Day(), h, m, s.loc)
					if at.After(t) {
						return at.UTC()
					}
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}
}

// NextN returns up to n occurrences after t.
func (s *Schedule) NextN(t time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	for len(out) < n {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}

// dayMatches applies cron's day rule: when both day of month and day of week are restricted, a day
// matching either one qualifies; otherwise both must match.
func (s *Schedule) dayMatches(day time.Time) bool {
	if s.spec.Month&(1<<uint(day.Month())) == 0 {
		return false
	}
	dom := s.spec.Dom&(1<<uint(day.Day())) > 0
	dow := s.spec.Dow&(1<<uint(day.Weekday())) > 0
	if s.spec.Dom&starBit > 0 || s.spec.Dow&starBit > 0 {
		return dom && dow
	}
	return dom || dow
}

// resolve turns a local wall time into an instant. An ambiguous wall time resolves to its first
// occurrence; a wall time inside a gap resolves to the first instant after the gap.
func resolve(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	wall := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	before := offset(wall.Add(-26*time.Hour), loc)
	after := offset(wall.Add(26*time.Hour), loc)
	var found time.Time
	for _, off := range []int{before, after} {
		at := wall.Add(-time.Duration(off) * time.Second)
		if !sameWall(at.In(loc), wall) {
			continue
		}
		if found.IsZero() || at.Before(found) {
			found = at
		}
	}
	if !found.IsZero() {
		return found
	}
	// A gap (the offset grows across it). wall-after lands before the transition and wall-before
	// lands after it; search between them for the first instant with the new offset.
	lo := wall.Add(-time.Duration(after) * time.Second)
	hi := wall.Add(-time.Duration(before) * time.Second)
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if offset(mid, loc) == after {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi.Truncate(time.Second)
}

func offset(t time.Time, loc *time.Location) int {
	_, off := t.In(loc).Zone()
	return off
}

func sameWall(local, wall time.Time) bool {
	return local.Year() == wall.Year() && local.Month() == wall.Month() && local.Day() == wall.Day() &&
		local.Hour() == wall.Hour() && local.Minute() == wall.Minute()
}
