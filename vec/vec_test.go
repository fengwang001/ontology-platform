package vec

import (
	"errors"
	"math"
	"testing"
)

func TestVecOps(t *testing.T) {
	cases := []struct {
		name   string
		a, b   []float64
		wantD  float64
		wantP  float64
		wantEr error
	}{
		{"basic", []float64{3, 4}, []float64{0, 0}, 5, 0, nil},
		{"orth", []float64{1, 0}, []float64{0, 1}, math.Sqrt2, 0, nil},
		{"neg", []float64{-1, -1}, []float64{2, 3}, 5, -5, nil},
		{"dim", []float64{1, 2, 3}, []float64{1, 2}, 0, 0, ErrDim},
		{"empty", nil, []float64{1}, 0, 0, ErrEmpty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := Dist(c.a, c.b); !errIs(err, c.wantEr) ||
				(c.wantEr == nil && math.Abs(got-c.wantD) > 1e-12) {
				t.Fatalf("Dist=%v err=%v want %v/%v", got, err, c.wantD, c.wantEr)
			}
			if got, err := Dot(c.a, c.b); !errIs(err, c.wantEr) ||
				(c.wantEr == nil && math.Abs(got-c.wantP) > 1e-12) {
				t.Fatalf("Dot=%v err=%v want %v/%v", got, err, c.wantP, c.wantEr)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		v    []float64
		want error
	}{
		{"ok", []float64{0, 1.5, -2}, nil},
		{"nan", []float64{1, math.NaN()}, ErrBadValue},
		{"pinf", []float64{math.Inf(1)}, ErrBadValue},
		{"ninf", []float64{math.Inf(-1)}, ErrBadValue},
		{"empty", []float64{}, ErrEmpty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.v); !errors.Is(err, c.want) {
				t.Fatalf("Validate err=%v want %v", err, c.want)
			}
		})
	}
}

func errIs(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}
