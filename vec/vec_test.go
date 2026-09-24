package vec

import (
	"errors"
	"math"
	"testing"
)

func TestGeometryAndValidation(t *testing.T) {
	cases := []struct {
		name   string
		x, y   Vec
		dot    float64
		dist   float64
		wanterr error
	}{
		{"basic", Vec{1, 2, 3}, Vec{4, 5, 6}, 32, math.Sqrt(27), nil},
		{"orthogonal", Vec{1, 0}, Vec{0, 1}, 0, math.Sqrt2, nil},
		{"dim1", Vec{3}, Vec{4}, 12, 1, nil},
		{"zero", Vec{0, 0}, Vec{0, 0}, 0, 0, nil},
		{"dim mismatch", Vec{1, 2}, Vec{1}, 0, 0, ErrDimMismatch},
		{"empty", Vec{}, Vec{1}, 0, 0, ErrInvalidVector},
		{"nan", Vec{1, math.NaN()}, Vec{1, 1}, 0, 0, ErrInvalidVector},
		{"posinf", Vec{1, math.Inf(1)}, Vec{1, 1}, 0, 0, ErrInvalidVector},
		{"neginf", Vec{1, math.Inf(-1)}, Vec{1, 1}, 0, 0, ErrInvalidVector},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dot, err := Dot(c.x, c.y)
			dist, err2 := Euclidean(c.x, c.y)
			if c.wanterr != nil {
				if !errors.Is(err, c.wanterr) || !errors.Is(err2, c.wanterr) {
					t.Fatalf("want %v, got dot-err %v dist-err %v", c.wanterr, err, err2)
				}
				if c.wanterr == ErrDimMismatch {
					if w, g, ok := WantGot(err); !ok || w != 2 || g != 1 {
						t.Fatalf("WantGot = %d,%d,%v", w, g, ok)
					}
				}
				return
			}
			if err != nil || err2 != nil {
				t.Fatalf("unexpected err %v %v", err, err2)
			}
			if math.Abs(dot-c.dot) > 1e-12 || math.Abs(dist-c.dist) > 1e-12 {
				t.Fatalf("dot=%v dist=%v want %v %v", dot, dist, c.dot, c.dist)
			}
		})
	}
}
