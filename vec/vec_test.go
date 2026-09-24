package vec

import (
	"errors"
	"math"
	"testing"
)

func TestOps(t *testing.T) {
	cases := []struct {
		name   string
		a, b   Vec
		dot    float64
		dist   float64
		dimErr bool
	}{
		{"basic", Vec{1, 2, 3}, Vec{4, 5, 6}, 32, 27, false},
		{"zeros", Vec{0, 0}, Vec{1, -1}, 0, 2, false},
		{"neg", Vec{-1, 2}, Vec{3, -2}, -7, 32, false},
		{"dim1", Vec{3}, Vec{4}, 12, 1, false},
		{"mismatch", Vec{1, 2}, Vec{1}, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dot, err := Dot(c.a, c.b)
			if c.dimErr {
				var de *DimError
				if !errors.As(err, &de) || !errors.Is(err, ErrDimMismatch) ||
					de.Want != len(c.a) || de.Got != len(c.b) {
					t.Fatalf("want DimError, got %v", err)
				}
				return
			}
			if err != nil || dot != c.dot {
				t.Fatalf("dot=%v err=%v want %v", dot, err, c.dot)
			}
			dist, err := SquaredDist(c.a, c.b)
			if err != nil || dist != c.dist {
				t.Fatalf("dist=%v err=%v want %v", dist, err, c.dist)
			}
		})
	}
}

func TestValidateAndCheckDim(t *testing.T) {
	cases := []struct {
		name string
		v    Vec
		want error
	}{
		{"ok", Vec{1, math.Copysign(0, -1), 2}, nil},
		{"empty", Vec{}, ErrEmpty},
		{"nan", Vec{1, math.NaN()}, ErrInvalidValue},
		{"posinf", Vec{math.Inf(1)}, ErrInvalidValue},
		{"neginf", Vec{math.Inf(-1)}, ErrInvalidValue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Validate(c.v); !errors.Is(got, c.want) {
				t.Fatalf("Validate=%v want %v", got, c.want)
			}
		})
	}
	if err := CheckDim(Vec{1, 2}, 3); !errors.Is(err, ErrDimMismatch) {
		t.Fatalf("CheckDim err=%v", err)
	}
}
