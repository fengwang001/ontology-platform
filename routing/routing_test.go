package routing_test

import (
	"errors"
	"testing"

	"ontology/routing"
)

func TestDefine(t *testing.T) {
	cases := []struct {
		name string
		id   string
		n    int
		back []int
		insp []bool
		r    int
		err  error
	}{
		{"ok", "a", 3, []int{1, 2, 2}, []bool{false, true, false}, 1, nil},
		{"self loop", "self", 1, []int{1}, []bool{true}, 0, nil},
		{"empty id", "", 2, []int{1, 2}, []bool{false, false}, 0, routing.ErrInvalid},
		{"n zero", "b", 0, nil, nil, 0, routing.ErrInvalid},
		{"n too big", "c", 33, make([]int, 33), make([]bool, 33), 0, routing.ErrInvalid},
		{"back too small", "d", 2, []int{0, 2}, []bool{false, false}, 0, routing.ErrInvalid},
		{"back beyond i", "e", 2, []int{1, 2}, []bool{false, false}, 0, nil},
		{"back beyond self", "f", 2, []int{2, 2}, []bool{false, false}, 0, routing.ErrInvalid},
		{"length mismatch back", "g", 3, []int{1, 2}, []bool{false, false, false}, 0, routing.ErrInvalid},
		{"length mismatch insp", "h", 3, []int{1, 2, 2}, []bool{false, false}, 0, routing.ErrInvalid},
		{"r negative", "i", 1, []int{1}, []bool{false}, -1, routing.ErrInvalid},
		{"r too big", "j", 1, []int{1}, []bool{false}, 1001, routing.ErrInvalid},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := routing.NewRegistry()
			rt, err := reg.Define(tc.id, tc.n, tc.back, tc.insp, tc.r)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
			if err == nil {
				if seen[tc.id] {
					t.Fatal("unexpected duplicate success")
				}
				seen[tc.id] = true
				got, err := reg.Get(tc.id)
				if err != nil || got != rt {
					t.Fatalf("get: %v %v", err, got)
				}
			}
		})
	}
}

func TestDefineConflict(t *testing.T) {
	reg := routing.NewRegistry()
	if _, err := reg.Define("dup", 1, []int{1}, []bool{false}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Define("dup", 1, []int{1}, []bool{false}, 0); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if _, err := reg.Get("missing"); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestBoundaries32(t *testing.T) {
	reg := routing.NewRegistry()
	back := make([]int, 32)
	insp := make([]bool, 32)
	for i := range back {
		back[i] = i + 1
	}
	rt, err := reg.Define("max", 32, back, insp, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if rt.N != 32 || rt.R != 1000 {
		t.Fatalf("got %+v", rt)
	}
}
