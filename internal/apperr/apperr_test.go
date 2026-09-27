package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsMatchesByCode(t *testing.T) {
	sentinel := New(KindUnauthenticated, "auth.invalid_credentials")
	wrapped := fmt.Errorf("login: %w", New(KindUnauthenticated, "auth.invalid_credentials"))
	if !errors.Is(wrapped, sentinel) {
		t.Fatal("errors.Is does not match equal codes")
	}
	if errors.Is(wrapped, New(KindUnauthenticated, "auth.locked")) {
		t.Fatal("errors.Is matches different codes")
	}
	e, ok := As(wrapped)
	if !ok || e.Kind != KindUnauthenticated {
		t.Fatalf("As: %v %v", e, ok)
	}
	inv := Invalid(Field("email", "validation.email"))
	if inv.Kind != KindInvalid || inv.Fields[0].Field != "email" || inv.Error() != "validation.failed" {
		t.Fatalf("%+v", inv)
	}
}
