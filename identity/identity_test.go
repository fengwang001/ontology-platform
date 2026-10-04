package identity

import (
	"errors"
	"testing"
)

func TestBindingAndRejectionOrder(t *testing.T) {
	t.Parallel()
	b := New()

	if _, err := b.Login(10, "", "u"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty device error = %v", err)
	}
	if _, err := b.Login(10, "d", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty user error = %v", err)
	}
	if _, err := b.Login(10, "d", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Login(11, "d", "u2"); !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("rebind error = %v, want already bound", err)
	}
	if _, err := b.Login(9, "d", "u2"); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("rebind stale error = %v, want clock skew", err)
	}
	if user := b.Subject("d"); user != "u" {
		t.Fatalf("subject = %q, want u", user)
	}

	if err := b.Logout(12, "other"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbound logout error = %v", err)
	}
	if err := b.Logout(9, "d"); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("bound stale logout error = %v, want clock skew", err)
	}
	if err := b.Logout(12, "d"); err != nil {
		t.Fatal(err)
	}
	if user := b.Subject("d"); user != "d" {
		t.Fatalf("subject after logout = %q, want d", user)
	}
}

func TestRejectedOperationDoesNotAdvanceClock(t *testing.T) {
	t.Parallel()
	b := New()
	if _, err := b.Login(10, "d", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Login(9, "d", "v"); !errors.Is(err, ErrClockSkew) {
		t.Fatal(err)
	}
	if err := b.Logout(8, "d"); !errors.Is(err, ErrClockSkew) {
		t.Fatal(err)
	}
	if _, err := b.Login(10, "e", "v"); err != nil {
		t.Fatalf("equal now after rejected stale op should be accepted: %v", err)
	}
}
