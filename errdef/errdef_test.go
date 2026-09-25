package errdef

import (
	"errors"
	"testing"
)

func TestTypedIs(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		want  error
		match bool
	}{
		{"not found sentinel", NotFound("missing user", WithObject("u1")), ErrNotFound, true},
		{"not found wide base", NotFound("x"), ErrBase, true},
		{"cross type mismatch", Conflict("x"), ErrNotFound, false},
		{"already exists", AlreadyExists("x"), ErrAlreadyExists, true},
		{"validation", Validation("x"), ErrValidation, true},
		{"permission", PermissionDenied("x"), ErrPermissionDenied, true},
		{"internal", Internal("x"), ErrInternal, true},
		{"unknown code generic", New(Code("future"), "x"), New(Code("future"), "y"), true},
		{"unknown code not typed", New(Code("future"), "x"), ErrNotFound, false},
		{"unknown code matches base", New(Code("future"), "x"), ErrBase, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errors.Is(tc.err, tc.want); got != tc.match {
				t.Fatalf("Is = %v, want %v", got, tc.match)
			}
		})
	}
}

func TestIsTraversesCauseChain(t *testing.T) {
	root := NotFound("missing", WithObject("obj"))
	mid := Internal("wrap1", WithCause(root))
	top := Conflict("wrap2", WithCause(mid))
	for _, target := range []error{ErrNotFound, ErrInternal, ErrConflict, ErrBase} {
		if !errors.Is(top, target) {
			t.Fatalf("chain must match %v", target)
		}
	}
	if errors.Is(top, ErrValidation) {
		t.Fatal("chain must not match absent code")
	}
}

func TestAsHierarchy(t *testing.T) {
	err := NotFound("x", WithProperty("name"))
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatal("As *NotFoundError failed")
	}
	if nf.Property() != "name" {
		t.Fatalf("property = %q", nf.Property())
	}
	var b *Base
	if !errors.As(err, &b) {
		t.Fatal("As *Base via embedded field failed")
	}
	if b.ErrCode() != CodeNotFound {
		t.Fatalf("code = %q", b.ErrCode())
	}
}

func TestIsStepsConstantAcrossDepth(t *testing.T) {
	for _, depth := range []int{100, 10000} {
		var err error = NotFound("deep")
		for range depth - 1 {
			err = NotFound("layer", WithCause(err))
		}
		ResetSteps()
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("depth %d: Is failed", depth)
		}
		if got := Steps(); got != 1 {
			t.Fatalf("depth %d: steps = %d, want 1 (outer self-code short circuit, independent of depth)", depth, got)
		}
	}
}

func TestContextPreserved(t *testing.T) {
	err := NotFound("x", WithObject("o9"), WithProperty("p2"))
	cp, ok := err.(ContextProvider)
	if !ok {
		t.Fatal("typed error must expose context")
	}
	if cp.Object() != "o9" || cp.Property() != "p2" {
		t.Fatalf("context = %q/%q", cp.Object(), cp.Property())
	}
}
