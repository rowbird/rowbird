package ids

import (
	"bytes"
	"testing"
)

func TestNewIsVersion7AndTimeOrdered(t *testing.T) {
	prev := New()
	for range 1000 {
		id := New()
		if id.Version() != 7 {
			t.Fatalf("version %d", id.Version())
		}
		if bytes.Compare(prev[:], id[:]) >= 0 {
			t.Fatalf("ids are not increasing: %s then %s", prev, id)
		}
		prev = id
	}
}

func TestParse(t *testing.T) {
	id := New()
	got, err := Parse(id.String())
	if err != nil || got != id {
		t.Fatalf("Parse round trip: %v", err)
	}
	if _, err := Parse("not-an-id"); err == nil {
		t.Fatal("accepted invalid id")
	}
}
