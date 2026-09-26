package errdef

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsHierarchy(t *testing.T) {
	cases := []struct {
		name   string
		code   Code
		target *Error
		want   bool
	}{
		{"not found matches sentinel", CodeNotFound, ErrNotFound, true},
		{"not found matches base", CodeNotFound, ErrBase, true},
		{"conflict matches base", CodeConflict, ErrBase, true},
		{"conflict not found mismatch", CodeConflict, ErrNotFound, false},
		{"validation vs permission mismatch", CodeValidation, ErrPermissionDenied, false},
		{"unknown base self match", CodeUnknown, ErrBase, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := New(tc.code, "msg")
			if got := errors.Is(err, tc.target); got != tc.want {
				t.Fatalf("Is = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCauseChainAndContext(t *testing.T) {
	inner := New(CodeInternal, "db down")
	outer := New(CodeNotFound, "missing",
		WithObject("Person"), WithProperty("age"), WithCause(inner))
	if !errors.Is(outer, ErrInternal) {
		t.Fatal("Is must traverse explicit cause")
	}
	if outer.Object() != "Person" || outer.Property() != "age" || outer.Cause() != error(inner) {
		t.Fatal("context/cause accessors mismatch")
	}
	if CauseDepth(outer) != 1 {
		t.Fatalf("depth = %d, want 1", CauseDepth(outer))
	}
}

func TestCompareCounterBoundedByComparisons(t *testing.T) {
	for _, layers := range []int{0, 1, 100, 10000} {
		t.Run(fmt.Sprintf("%d layers", layers), func(t *testing.T) {
			err := error(New(CodeNotFound, "x"))
			for i := 0; i < layers; i++ {
				err = fmt.Errorf("wrap %d: %w", i, err)
			}
			ResetCompareCount()
			if !errors.Is(err, ErrNotFound) {
				t.Fatal("expected match at chain end")
			}
			if got := IsCompareCount(); got != 1 {
				t.Fatalf("counter = %d, want 1 regardless of %d layers", got, layers)
			}
		})
	}
}

func TestEmptyCodeNormalizesToUnknown(t *testing.T) {
	if New("", "x").Code() != CodeUnknown {
		t.Fatal("empty code must normalize to unknown")
	}
}
