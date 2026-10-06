package plan

import (
	"errors"
	"testing"
)

func TestNext(t *testing.T) {
	p, err := New(10, []int{7, 3, 5}) // unsorted input is normalized
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct{ v, want int }{
		{1, 3},
		{2, 3},
		{3, 5}, // exactly on a mandatory version: not repeated
		{4, 5},
		{5, 7},
		{6, 7},
		{7, 10},
		{9, 10},
		{10, 10},
	}
	for _, tc := range cases {
		if got := p.Next(tc.v); got != tc.want {
			t.Fatalf("Next(%d) = %d, want %d", tc.v, got, tc.want)
		}
	}

	bare, err := New(2, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := bare.Next(1); got != 2 {
		t.Fatalf("Next(1) = %d, want 2 (no mandatory versions)", got)
	}
	if bare.Target() != 2 {
		t.Fatalf("Target() = %d, want 2", bare.Target())
	}
}

func TestNewValidation(t *testing.T) {
	full := make([]int, 64)
	for i := range full {
		full[i] = i + 1
	}
	tooMany := make([]int, 65)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	cases := []struct {
		name string
		T    int
		M    []int
		want error
	}{
		{"target too small", 1, nil, ErrInvalid},
		{"target zero", 0, nil, ErrInvalid},
		{"target too large", 1_000_001, nil, ErrInvalid},
		{"too many mandatory", 1_000_000, tooMany, ErrInvalid},
		{"duplicate mandatory", 10, []int{3, 3}, ErrInvalid},
		{"zero mandatory", 10, []int{0}, ErrInvalid},
		{"negative mandatory", 10, []int{-1}, ErrInvalid},
		{"mandatory equals target", 10, []int{10}, ErrInvalid},
		{"mandatory above target", 10, []int{11}, ErrInvalid},
		{"min target", 2, nil, nil},
		{"max target with full set", 1_000_000, full, nil},
	}
	for _, tc := range cases {
		_, err := New(tc.T, tc.M)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: New(%d,%v) err = %v, want %v", tc.name, tc.T, tc.M, err, tc.want)
		}
	}
}
