package local

import (
	"testing"

	"github.com/rowbird/rowbird/internal/storage/storagetest"
)

func TestConformance(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storagetest.Run(t, s, "")
}
