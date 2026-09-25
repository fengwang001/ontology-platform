package aggregate

import (
	"errors"
	"testing"

	"ontology/errdef"
	"ontology/wrap"
)

func TestAggregateIsAnyChild(t *testing.T) {
	agg := New(
		errdef.NotFound("a", errdef.WithObject("o1")),
		errdef.Conflict("b", errdef.WithObject("o2")),
	)
	cases := []struct {
		name   string
		target error
		match  bool
	}{
		{"first child", errdef.ErrNotFound, true},
		{"second child", errdef.ErrConflict, true},
		{"wide base", errdef.ErrBase, true},
		{"absent type", errdef.ErrValidation, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errors.Is(agg, tc.target); got != tc.match {
				t.Fatalf("Is = %v, want %v", got, tc.match)
			}
		})
	}
}

func TestAggregateIndexAndOrder(t *testing.T) {
	in := []error{
		errdef.NotFound("first", errdef.WithObject("o1")),
		wrap.Wrap(errdef.Conflict("second"), wrap.Ctx{Object: "o2"}),
		errdef.Validation("third"),
	}
	a := New(in...).(Aggregate)
	if a.Len() != len(in) {
		t.Fatalf("Len = %d, want %d (no child may be lost)", a.Len(), len(in))
	}
	for i := range in {
		if a.At(i).Error() != in[i].Error() {
			t.Fatalf("index %d not byte-equal in order", i)
		}
	}
	for i, e := range a.All() {
		if e.Error() != in[i].Error() {
			t.Fatalf("All order mismatch at %d", i)
		}
	}
}

func TestAggregateAsAndUnwrap(t *testing.T) {
	a := New(errdef.NotFound("x"), errdef.Conflict("y")).(Aggregate)
	var c *errdef.ConflictError
	if !errors.As(a, &c) {
		t.Fatal("As must find matching child")
	}
	if len(a.Unwrap()) != 2 {
		t.Fatal("Unwrap must expose all children")
	}
}

func TestNewDropsNil(t *testing.T) {
	if New(nil, nil) != nil {
		t.Fatal("all-nil batch must be nil")
	}
	a := New(nil, errdef.Internal("x")).(Aggregate)
	if a.Len() != 1 {
		t.Fatalf("Len = %d, want 1", a.Len())
	}
}
