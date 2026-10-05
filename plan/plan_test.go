package plan

import "testing"

func TestNewInvalid(t *testing.T) {
	cases := []struct {
		name string
		t    int
		m    []int
	}{
		{"target too small", 1, nil},
		{"target too big", 1_000_001, nil},
		{"zero hop", 10, []int{0}},
		{"negative hop", 10, []int{-3}},
		{"hop equals target", 10, []int{10}},
		{"hop above target", 10, []int{11}},
		{"duplicate hop", 10, []int{3, 5, 3}},
		{"too many hops", 100, make([]int, 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.t, tc.m); err != ErrInvalid {
				t.Fatalf("New(%d, %v) = %v, want ErrInvalid", tc.t, tc.m, err)
			}
		})
	}
}

func TestNext(t *testing.T) {
	p, err := New(6, []int{5, 3}) // unsorted input must be normalized
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		v, want int
	}{
		{1, 3},
		{2, 3},
		{3, 5}, // exactly on a mandatory version: never repeated
		{4, 5},
		{5, 6},
		{6, 6},
	}
	for _, tc := range cases {
		if got := p.Next(tc.v); got != tc.want {
			t.Fatalf("Next(%d) = %d, want %d", tc.v, got, tc.want)
		}
	}
	if p.Target() != 6 {
		t.Fatalf("Target() = %d, want 6", p.Target())
	}
}

func TestNextNoMandatory(t *testing.T) {
	p, err := New(9, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, v := range []int{1, 4, 8, 9} {
		if got := p.Next(v); got != 9 {
			t.Fatalf("Next(%d) = %d, want 9", v, got)
		}
	}
}
