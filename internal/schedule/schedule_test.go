package schedule

import (
	"errors"
	"testing"
	"time"
)

func mustParse(t *testing.T, expr, tz string) *Schedule {
	t.Helper()
	s, err := Parse(expr, tz)
	if err != nil {
		t.Fatalf("Parse(%q, %q): %v", expr, tz, err)
	}
	return s
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		expr, tz string
		want     error
	}{
		{"", "UTC", ErrInvalidExpression},
		{"* * * *", "UTC", ErrInvalidExpression},
		{"0 * * * * *", "UTC", ErrInvalidExpression},
		{"@every 1m", "UTC", ErrInvalidExpression},
		{"@reboot", "UTC", ErrInvalidExpression},
		{"CRON_TZ=UTC 0 * * * *", "UTC", ErrInvalidExpression},
		{"TZ=UTC 0 * * * *", "UTC", ErrInvalidExpression},
		{"61 * * * *", "UTC", ErrInvalidExpression},
		{"0 0 30 2 *", "UTC", ErrNeverRuns},
		{"0 0 * * *", "", ErrInvalidTimezone},
		{"0 0 * * *", "Local", ErrInvalidTimezone},
		{"0 0 * * *", "Mars/Olympus", ErrInvalidTimezone},
	}
	for _, tc := range cases {
		if _, err := Parse(tc.expr, tc.tz); !errors.Is(err, tc.want) {
			t.Errorf("Parse(%q, %q) = %v, want %v", tc.expr, tc.tz, err, tc.want)
		}
	}
}

func TestParseAccepts(t *testing.T) {
	for _, expr := range []string{"* * * * *", "*/5 9-18 * * 1-5", "0 7 * * MON-FRI", "@daily", "@Weekly", "  0   8 1 * * "} {
		if _, err := Parse(expr, "America/Sao_Paulo"); err != nil {
			t.Errorf("Parse(%q): %v", expr, err)
		}
	}
	if s := mustParse(t, "  0   8 1 * * ", "UTC"); s.Expression() != "0 8 1 * *" {
		t.Errorf("normalized = %q", s.Expression())
	}
}

func TestNext(t *testing.T) {
	cases := []struct {
		name, expr, tz, after string
		want                  []string
	}{
		{"every minute", "* * * * *", "UTC", "2026-03-10T10:00:30Z", []string{"2026-03-10T10:01:00Z", "2026-03-10T10:02:00Z"}},
		{"strictly after", "0 7 * * *", "UTC", "2026-03-10T07:00:00Z", []string{"2026-03-11T07:00:00Z"}},
		{"weekdays in Sao Paulo", "0 7 * * 1-5", "America/Sao_Paulo", "2026-09-25T12:00:00Z", []string{"2026-09-28T10:00:00Z", "2026-09-29T10:00:00Z"}},
		{"day 31 skips short months", "0 0 31 * *", "UTC", "2026-01-31T00:00:00Z", []string{"2026-03-31T00:00:00Z", "2026-05-31T00:00:00Z"}},
		{"29 February", "0 12 29 2 *", "UTC", "2025-01-01T00:00:00Z", []string{"2028-02-29T12:00:00Z", "2032-02-29T12:00:00Z"}},
		{"weekly descriptor is Sunday midnight", "@weekly", "Europe/London", "2026-09-25T00:00:00Z", []string{"2026-09-26T23:00:00Z"}},
		{"day of month or day of week", "0 0 1 * 1", "UTC", "2026-09-25T00:00:00Z", []string{"2026-09-28T00:00:00Z", "2026-10-01T00:00:00Z", "2026-10-05T00:00:00Z"}},
		// New York springs forward on 2026-03-08 at 02:00 (to 03:00). 02:30 does not exist and runs at
		// the first instant after the gap, 03:00 EDT (07:00Z), once.
		{"new york gap", "30 2 * * *", "America/New_York", "2026-03-07T12:00:00Z", []string{"2026-03-08T07:00:00Z", "2026-03-09T06:30:00Z"}},
		{"new york gap every 15 minutes runs once", "*/15 2 * * *", "America/New_York", "2026-03-08T06:00:00Z", []string{"2026-03-08T07:00:00Z", "2026-03-09T06:00:00Z"}},
		// New York falls back on 2026-11-01 at 02:00 (to 01:00). 01:30 happens twice and runs once, at
		// the first occurrence (05:30Z, EDT).
		{"new york ambiguous", "30 1 * * *", "America/New_York", "2026-10-31T12:00:00Z", []string{"2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z"}},
		{"new york ambiguous after first pass", "30 1 * * *", "America/New_York", "2026-11-01T05:45:00Z", []string{"2026-11-02T06:30:00Z"}},
		// London: gap on 2026-03-29 01:00 -> 02:00, repeat on 2026-10-25 02:00 -> 01:00.
		{"london gap", "30 1 * * *", "Europe/London", "2026-03-28T12:00:00Z", []string{"2026-03-29T01:00:00Z", "2026-03-30T00:30:00Z"}},
		{"london ambiguous", "30 1 * * *", "Europe/London", "2026-10-24T12:00:00Z", []string{"2026-10-25T00:30:00Z", "2026-10-26T01:30:00Z"}},
		// Sao Paulo had daylight saving until 2019; in 2018 it started at midnight on 4 November, so
		// 00:00 did not exist and midnight jobs ran at 01:00 -02 (03:00Z).
		{"sao paulo midnight gap", "0 0 * * *", "America/Sao_Paulo", "2018-11-03T12:00:00Z", []string{"2018-11-04T03:00:00Z", "2018-11-05T02:00:00Z"}},
		{"sao paulo end of dst", "30 23 * * *", "America/Sao_Paulo", "2019-02-16T12:00:00Z", []string{"2019-02-17T01:30:00Z", "2019-02-18T02:30:00Z"}},
		{"hourly across fall back", "0 * * * *", "America/New_York", "2026-11-01T04:30:00Z", []string{"2026-11-01T05:00:00Z", "2026-11-01T07:00:00Z", "2026-11-01T08:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustParse(t, tc.expr, tc.tz)
			got := s.NextN(utc(tc.after), len(tc.want))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d occurrences, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if !got[i].Equal(utc(w)) || got[i].Location() != time.UTC {
					t.Errorf("occurrence %d = %s, want %s", i, got[i].Format(time.RFC3339), w)
				}
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct{ expr, locale, want string }{
		{"*/5 * * * *", "en", "Every 5 minutes"},
		{"0 7 * * 1-5", "en", "At 07:00, Monday through Friday"},
		{"0 7 * * 1-5", "pt-BR", "Às 07:00, de segunda-feira a sexta-feira"},
		{"@daily", "en", "At 00:00"},
		{"0 9 * * *", "fr", "At 09:00"},
	}
	for _, tc := range cases {
		if got := Describe(tc.expr, tc.locale); got != tc.want {
			t.Errorf("Describe(%q, %q) = %q, want %q", tc.expr, tc.locale, got, tc.want)
		}
	}
}
