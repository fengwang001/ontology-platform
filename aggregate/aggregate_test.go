package aggregate

import (
	"errors"
	"testing"

	"ontology/errdef"
)

func TestIsMatchesAnyChild(t *testing.T) {
	cases := []struct {
		name   string
		code   errdef.Code
		target *errdef.Error
		want   bool
	}{
		{"first child", errdef.CodeNotFound, errdef.ErrNotFound, true},
		{"second child", errdef.CodeConflict, errdef.ErrConflict, true},
		{"base matches", errdef.CodeInternal, errdef.ErrBase, true},
		{"absent type", errdef.CodeValidation, errdef.ErrValidation, false},
	}
	children := []error{
		errdef.New(errdef.CodeNotFound, "a", errdef.WithObject("P1")),
		errdef.New(errdef.CodeConflict, "b", errdef.WithObject("P2")),
	}
	agg := New(children...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errors.Is(agg, tc.target) != tc.want {
				t.Fatalf("Is any-child = %v, want %v", !tc.want, tc.want)
			}
		})
	}
}

func TestIndexableAndOrderPreserved(t *testing.T) {
	sizes := []int{0, 1, 2, 8}
	for _, n := range sizes {
		children := make([]error, 0, n)
		for i := 0; i < n; i++ {
			children = append(children,
				errdef.New(errdef.CodeNotFound, "e", errdef.WithObject(string(rune('a'+i)))))
		}
		agg := New(children...)
		if n == 0 {
			if agg != nil {
				t.Fatalf("n=%d: empty aggregate must be nil", n)
			}
			continue
		}
		if agg.Len() != n {
			t.Fatalf("Len = %d, want %d", agg.Len(), n)
		}
		for i := 0; i < n; i++ {
			got, ok := agg.At(i)
			if !ok || got != children[i] {
				t.Fatalf("At(%d) order mismatch", i)
			}
		}
		if _, ok := agg.At(n); ok {
			t.Fatal("At(len) must fail")
		}
		seen := 0
		agg.Range(func(i int, err error) bool {
			if err != children[i] {
				t.Fatalf("Range order mismatch at %d", i)
			}
			seen++
			return true
		})
		if seen != n {
			t.Fatalf("Range visited %d, want %d", seen, n)
		}
	}
}

func TestNestedAggregateAndNils(t *testing.T) {
	inner := New(errdef.New(errdef.CodeInternal, "x"))
	outer := New(inner, errdef.New(errdef.CodeConflict, "y"))
	if !errors.Is(outer, errdef.ErrInternal) {
		t.Fatal("Is must traverse nested aggregates")
	}
	if New(nil, nil, nil) != nil {
		t.Fatal("all-nil children must yield nil")
	}
	var asAgg *Aggregate
	if !errors.As(error(outer), &asAgg) || asAgg.Len() != 2 {
		t.Fatal("As must find aggregate")
	}
}
