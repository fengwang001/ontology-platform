package wrap

import (
	"fmt"
	"testing"

	"ontology/errdef"
)

func TestWrapPiercesType(t *testing.T) {
	cases := []struct {
		name   string
		code   errdef.Code
		target *errdef.Error
		want   bool
	}{
		{"not found pierces", errdef.CodeNotFound, errdef.ErrNotFound, true},
		{"conflict pierces", errdef.CodeConflict, errdef.ErrConflict, true},
		{"base pierces", errdef.CodeValidation, errdef.ErrBase, true},
		{"mismatch stays false", errdef.CodeValidation, errdef.ErrConflict, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := Wrap(errdef.New(tc.code, "m"),
				WithObject("o"), WithProperty("p"))
			if Is(w, tc.target) != tc.want {
				t.Fatalf("Is = %v, want %v", !tc.want, tc.want)
			}
			var typed *errdef.Error
			if As(w, &typed) != true || typed.Code() != tc.code {
				t.Fatal("As must reach typed error")
			}
		})
	}
}

func TestWrapSemantics(t *testing.T) {
	inner := errdef.New(errdef.CodeNotFound, "m")
	if Wrap(nil) != nil {
		t.Fatal("Wrap(nil) must be nil")
	}
	w := Wrap(inner, WithObject("Person"), WithProperty("age"))
	if Unwrap(w) != error(inner) {
		t.Fatal("Unwrap must expose original cause")
	}
	obj, prop, ok := ContextOf(w)
	if !ok || obj != "Person" || prop != "age" {
		t.Fatalf("ContextOf = %q,%q,%v", obj, prop, ok)
	}
	if _, _, ok := ContextOf(inner); ok {
		t.Fatal("typed error must not be mistaken for a wrapper")
	}
}

func TestDepthAcrossLayers(t *testing.T) {
	for _, layers := range []int{1, 100, 10000} {
		t.Run(fmt.Sprintf("%d layers", layers), func(t *testing.T) {
			var err error = errdef.New(errdef.CodeNotFound, "x")
			for i := 0; i < layers; i++ {
				err = Wrap(err)
			}
			if Depth(err) != layers {
				t.Fatalf("depth = %d, want %d", Depth(err), layers)
			}
			if !Is(err, errdef.ErrNotFound) {
				t.Fatal("Is must match at full depth")
			}
		})
	}
}
