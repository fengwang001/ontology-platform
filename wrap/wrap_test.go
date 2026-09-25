package wrap

import (
	"errors"
	"testing"

	"ontology/errdef"
)

func TestWrapIs(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		target error
		match  bool
	}{
		{"wrapped keeps type", Wrap(errdef.NotFound("x"), Ctx{Object: "o1"}), errdef.ErrNotFound, true},
		{"wrapped matches base", Wrap(errdef.Conflict("x")), errdef.ErrBase, true},
		{"wrapped wrong type", Wrap(errdef.Validation("x")), errdef.ErrConflict, false},
		{"double wrap pierces", Wrap(Wrap(errdef.PermissionDenied("x"), Ctx{Object: "a"}), Ctx{Property: "p"}), errdef.ErrPermissionDenied, true},
		{"wrap plain error falls back internal", Wrap(errors.New("boom")), errdef.ErrInternal, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errors.Is(tc.err, tc.target); got != tc.match {
				t.Fatalf("Is = %v, want %v", got, tc.match)
			}
		})
	}
}

func TestWrapContext(t *testing.T) {
	w := Wrap(errdef.NotFound("x"), Ctx{Object: "obj", Property: "prop"})
	cp := w.(errdef.ContextProvider)
	if cp.Object() != "obj" || cp.Property() != "prop" {
		t.Fatalf("context = %q/%q", cp.Object(), cp.Property())
	}
	var b *errdef.Base
	if !errors.As(w, &b) {
		t.Fatal("As must pierce wrap to underlying *Base")
	}
}

func TestWrapUnwrapExposesCause(t *testing.T) {
	root := errdef.Conflict("root")
	w := Wrap(root, Ctx{Object: "o"})
	if !errors.Is(errors.Unwrap(w), root) {
		t.Fatal("Unwrap must expose the original cause identity")
	}
}

func TestWrapStepsConstantAcrossDepth(t *testing.T) {
	for _, depth := range []int{100, 10000} {
		var e error = errdef.NotFound("deep")
		for range depth {
			e = Wrap(e, Ctx{Object: "o"})
		}
		ResetSteps()
		if !errors.Is(e, errdef.ErrNotFound) {
			t.Fatalf("depth %d: Is failed", depth)
		}
		if got := Steps(); got != 1 {
			t.Fatalf("depth %d: steps = %d, want 1", depth, got)
		}
	}
}
