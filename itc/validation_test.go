package itc

import (
	"errors"
	"testing"
)

// TestValidationOrdering checks the documented rejection reasons and that
// rejected operations never mutate state.
func TestValidationOrdering(t *testing.T) {
	r := NewRegistry()
	if err := r.Seed(""); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty seed: %v", err)
	}
	must(t, r.Seed("a"))
	if err := r.Seed("a"); !errors.Is(err, ErrNameExists) {
		t.Fatalf("duplicate seed: %v", err)
	}
	if _, _, err := r.Fork("a", ""); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("fork empty child: %v", err)
	}
	if _, _, err := r.Fork("nope", "c"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("fork unknown parent: %v", err)
	}
	if _, _, err := r.Fork("a", "a"); !errors.Is(err, ErrNameExists) {
		t.Fatalf("fork existing child: %v", err)
	}
	if _, err := r.Join("a", "a"); !errors.Is(err, ErrSameName) {
		t.Fatalf("join self: %v", err)
	}
	if _, err := r.Join("nope", "a"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("join unknown a: %v", err)
	}
	if _, err := r.Join("a", "nope"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("join unknown b: %v", err)
	}
	if err := r.Event("nope"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("event unknown: %v", err)
	}
	if _, err := r.Peek("nope"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("peek unknown: %v", err)
	}
	if _, err := r.Compare("nope", "a"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("compare unknown first: %v", err)
	}
	if _, err := r.Compare("a", "nope"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("compare unknown second: %v", err)
	}
	if got := mustString(t, r, "a"); got != "(1;0)" {
		t.Fatalf("state mutated by rejected ops: %s", got)
	}

	// After a successful join the merged-away name is unknown.
	_, _, err := r.Fork("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := r.Event("b"); !errors.Is(err, ErrUnknownName) {
		t.Fatalf("joined-away replica must be unknown: %v", err)
	}
}

// TestPeekSnapshot checks the (0;event) form and that a returned snapshot is
// an immutable string, never aliased to mutable internal state.
func TestPeekSnapshot(t *testing.T) {
	r := NewRegistry()
	must(t, r.Seed("a"))
	must(t, r.Event("a"))
	p, err := r.Peek("a")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(`Peek("a") = %s (basis: identity erased to 0, event copied)`, p)
	if p != "(0;1)" {
		t.Fatalf("peek: got %q want %q", p, "(0;1)")
	}
	must(t, r.Event("a"))
	p2, _ := r.Peek("a")
	if p2 == p {
		t.Fatal("later events must not leak into an earlier snapshot")
	}
	if p2 != "(0;2)" {
		t.Fatalf("peek after second event: got %q", p2)
	}
}
