package mssql

import (
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
)

func TestFormatGUID(t *testing.T) {
	// SQL Server stores A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11 with the first three groups byte-swapped.
	raw := []byte{0x99, 0xBC, 0xEE, 0xA0, 0x0B, 0x9C, 0xF8, 0x4E, 0xBB, 0x6D, 0x6B, 0xB9, 0xBD, 0x38, 0x0A, 0x11}
	if got := formatGUID(raw); got != "A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11" {
		t.Fatalf("got %s", got)
	}
}

func TestTypes(t *testing.T) {
	cases := map[string]struct {
		want plugin.ValueType
		tz   bool
	}{
		"int": {plugin.TypeInt, false}, "BIGINT": {plugin.TypeInt, false}, "decimal": {plugin.TypeDecimal, false},
		"money": {plugin.TypeDecimal, false}, "float": {plugin.TypeFloat, false}, "bit": {plugin.TypeBool, false},
		"date": {plugin.TypeDate, false}, "datetime2": {plugin.TypeDateTime, false},
		"datetimeoffset": {plugin.TypeDateTime, true}, "time": {plugin.TypeTime, false},
		"varbinary": {plugin.TypeBinary, false}, "nvarchar": {plugin.TypeText, false},
		"uniqueidentifier": {plugin.TypeText, false}, "geography": {plugin.TypeUnknown, false},
	}
	for name, tc := range cases {
		if got, tz := typeOf(name); got != tc.want || tz != tc.tz {
			t.Errorf("%s: got %s tz=%v", name, got, tz)
		}
	}
	if dbType("nvarchar", 40, 0, 0) != "nvarchar(20)" || dbType("varchar", -1, 0, 0) != "varchar(max)" || dbType("decimal", 17, 38, 9) != "decimal(38,9)" {
		t.Fatal("dbType")
	}
}

func TestConvert(t *testing.T) {
	d := driver{}
	cases := []struct {
		col  plugin.Column
		in   any
		want any
	}{
		{plugin.Column{Type: plugin.TypeDecimal}, []byte("12345678901234567890123456789.123456789"), plugin.Decimal("12345678901234567890123456789.123456789")},
		{plugin.Column{Type: plugin.TypeInt}, int32(7), int64(7)},
		{plugin.Column{Type: plugin.TypeTime}, time.Date(1, 1, 1, 10, 11, 12, 500000000, time.UTC), plugin.TimeOfDay("10:11:12.5")},
		{plugin.Column{Type: plugin.TypeDate}, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), plugin.Date("2026-09-25")},
		{plugin.Column{Type: plugin.TypeDateTime, WithTimeZone: true}, time.Date(2026, 9, 25, 13, 11, 12, 0, time.FixedZone("", 3*3600)), time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
	}
	for _, tc := range cases {
		got, err := d.Convert(tc.col, tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if gt, ok := got.(time.Time); ok {
			if !gt.Equal(tc.want.(time.Time)) || gt.Location() != time.UTC {
				t.Errorf("%v: got %v", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%v: got %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
