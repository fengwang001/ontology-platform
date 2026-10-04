package routing

import (
	"errors"
	"testing"
)

func TestDefineAndGet(t *testing.T) {
	cases := []struct {
		name  string
		route string
		n     int
		back  []int
		insp  []bool
		R     int
		want  error
	}{
		{"ok", "r1", 3, []int{1, 2, 2}, []bool{false, true, false}, 1, nil},
		{"empty route", "", 2, []int{1, 2}, []bool{false, false}, 0, ErrInvalid},
		{"n zero", "r2", 0, nil, nil, 0, ErrInvalid},
		{"n too big", "r3", 33, make([]int, 33), make([]bool, 33), 0, ErrInvalid},
		{"R negative", "r4", 2, []int{1, 2}, []bool{false, false}, -1, ErrInvalid},
		{"R too big", "r5", 2, []int{1, 2}, []bool{false, false}, 1001, ErrInvalid},
		{"back len mismatch", "r6", 2, []int{1}, []bool{false, false}, 0, ErrInvalid},
		{"insp len mismatch", "r7", 2, []int{1, 2}, []bool{false}, 0, ErrInvalid},
		{"back zero", "r8", 2, []int{0, 2}, []bool{false, false}, 0, ErrInvalid},
		{"back forward", "r9", 2, []int{1, 3}, []bool{false, false}, 0, ErrInvalid},
		{"R=1000 ok", "r10", 1, []int{1}, []bool{true}, 1000, nil},
		{"n=32 ok", "r11", 32, backSelf(32), make([]bool, 32), 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager()
			if got := m.Define(tc.route, tc.n, tc.back, tc.insp, tc.R); !errors.Is(got, tc.want) {
				t.Fatalf("Define = %v, want %v", got, tc.want)
			}
			if tc.want == nil {
				r, err := m.Get(tc.route)
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				if r.N != tc.n || r.R != tc.R {
					t.Fatalf("got %+v", r)
				}
			}
		})
	}

	t.Run("duplicate route conflicts", func(t *testing.T) {
		m := NewManager()
		if err := m.Define("dup", 1, []int{1}, []bool{false}, 0); err != nil {
			t.Fatal(err)
		}
		if err := m.Define("dup", 1, []int{1}, []bool{false}, 0); !errors.Is(err, ErrState) {
			t.Fatalf("second Define = %v, want ErrState", err)
		}
	})

	t.Run("get missing and empty", func(t *testing.T) {
		m := NewManager()
		if _, err := m.Get("ghost"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get ghost = %v", err)
		}
		if _, err := m.Get(""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Get empty = %v", err)
		}
	})

	t.Run("defensive copy", func(t *testing.T) {
		m := NewManager()
		back := []int{1, 2}
		insp := []bool{false, true}
		if err := m.Define("r", 2, back, insp, 0); err != nil {
			t.Fatal(err)
		}
		back[1] = 1
		insp[0] = true
		r, _ := m.Get("r")
		if r.Back[1] != 2 || r.Insp[0] {
			t.Fatalf("route aliases caller slices: %+v", r)
		}
	})
}

func backSelf(n int) []int {
	b := make([]int, n)
	for i := range b {
		b[i] = i + 1
	}
	return b
}
