package vec

import (
	"errors"
	"math"
	"testing"
)

func TestVecOps(t *testing.T) {
	cases := []struct {
		name   string
		a, b   Vector
		dot    float64
		dist   float64
		wantOK bool
	}{
		{"basic", Vector{1, 2, 3}, Vector{4, 5, 6}, 32, 27, true},
		{"zero", Vector{0, 0}, Vector{1, -1}, 0, 2, true},
		{"neg", Vector{-1, 2}, Vector{3, -4}, -11, 52, true},
		{"dim1", Vector{3}, Vector{3}, 9, 0, true},
		{"mismatch", Vector{1}, Vector{1, 2}, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := Dot(c.a, c.b)
			if c.wantOK != (err == nil) {
				t.Fatalf("dot err=%v", err)
			}
			if err == nil {
				if d != c.dot {
					t.Fatalf("dot=%v want %v", d, c.dot)
				}
				sd, err := SquaredDist(c.a, c.b)
				if err != nil || sd != c.dist {
					t.Fatalf("dist=%v err=%v want %v", sd, err, c.dist)
				}
			} else if !errors.Is(err, ErrDimMismatch) {
				t.Fatalf("want ErrDimMismatch, got %v", err)
			}
		})
	}
	if err := CheckDim(2, 3); !errors.Is(err, ErrDimMismatch) {
		t.Fatalf("CheckDim err=%v", err)
	}
	bad := []Vector{{math.NaN()}, {math.Inf(1)}, {math.Inf(-1)}}
	for i, v := range bad {
		if !errors.Is(v.Validate(), ErrInvalidValue) {
			t.Fatalf("case %d not rejected", i)
		}
	}
	if (Vector{0, 0}).Validate() != nil || !(Vector{0, 0}).IsZero() {
		t.Fatal("zero vector must be valid and IsZero")
	}
	if (Vector{1}).Dim() != 1 {
		t.Fatal("dim")
	}
}
