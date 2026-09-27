package plugin

import (
	"math"
	"testing"
	"time"
)

func TestJSONValue(t *testing.T) {
	ts := time.Date(2026, 9, 25, 10, 11, 12, 500, time.UTC)
	cases := []struct {
		in, want any
	}{
		{int64(42), int64(42)},
		{int64(1 << 53), "9007199254740992"},
		{int64(-(1 << 53)), "-9007199254740992"},
		{math.Inf(1), "+Inf"},
		{Decimal("1.10"), "1.10"},
		{Date("2026-09-25"), "2026-09-25"},
		{ts, "2026-09-25T10:11:12.0000005Z"},
		{nil, nil},
		{"x", "x"},
		{true, true},
	}
	for _, tc := range cases {
		if got := JSONValue(tc.in); got != tc.want {
			t.Errorf("%#v: got %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
