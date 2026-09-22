package sparse

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func TestCosineZeroNorm(t *testing.T) {
	cases := []struct {
		name    string
		a, b    Vector
		vecWant int
	}{
		{"empty left", Vector{}, Vector{{0, 1}}, 0},
		{"empty right", Vector{{0, 1}}, Vector{}, 1},
		{"all-zero left", Vector{{0, 0}, {5, 0}}, Vector{{0, 1}}, 0},
		{"all-zero right", Vector{{0, 1}}, Vector{{3, 0}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Cosine(tc.a, tc.b)
			var ze *ZeroNormError
			if !errors.As(err, &ze) {
				t.Fatalf("err = %v, want *ZeroNormError", err)
			}
			if ze.Vector != tc.vecWant {
				t.Fatalf("ZeroNormError.Vector = %d, want %d", ze.Vector, tc.vecWant)
			}
		})
	}
}

func TestCosineIdenticalVectorsExactlyOne(t *testing.T) {
	vectors := []Vector{
		{{0, 1}},
		{{0, 0.1}, {7, 0.3}, {42, -0.7}},
		{{1, 1e16}, {2, 1}, {3, -1e16}},
		{{0, math.Pi}, {9, math.E}},
	}
	for i, v := range vectors {
		got, _, err := Cosine(v, v)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got != 1 {
			t.Fatalf("case %d: cosine = %v, want exactly 1", i, got)
		}
	}
}

func TestCosineClampedToUnitInterval(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 1000; trial++ {
		a := randomVector(rng, 8)
		b := randomVector(rng, 8)
		got, _, err := Cosine(a, b)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if got < -1 || got > 1 {
			t.Fatalf("trial %d: cosine %v outside [-1, 1]", trial, got)
		}
	}
}

func randomVector(rng *rand.Rand, n int) Vector {
	v := make(Vector, n)
	for i := range v {
		v[i] = Entry{Index: uint32(i * 2), Value: rng.NormFloat64()}
	}
	return v
}

func TestDotInfNonFiniteError(t *testing.T) {
	a := Vector{{0, math.Inf(1)}}
	b := Vector{{0, 2}}

	_, _, err := Dot(a, b)
	var nfe *NonFiniteError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v, want *NonFiniteError", err)
	}
}

func TestDotInfNaNNonFiniteError(t *testing.T) {
	a := Vector{{0, math.Inf(1)}, {1, math.Inf(-1)}}
	b := Vector{{0, 1}, {1, 1}} // +Inf + -Inf => NaN

	got, _, err := Dot(a, b)
	var nfe *NonFiniteError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v, want *NonFiniteError", err)
	}
	if !math.IsNaN(got) {
		t.Fatalf("got = %v, want NaN alongside the error", got)
	}
}

func TestCosineInfNonFiniteError(t *testing.T) {
	a := Vector{{0, math.Inf(-1)}}
	b := Vector{{0, 3}}

	_, _, err := Cosine(a, b)
	var nfe *NonFiniteError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v, want *NonFiniteError", err)
	}
}
