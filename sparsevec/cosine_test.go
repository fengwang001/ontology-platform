package sparsevec

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func TestCosineIdenticalVectorsExactlyOne(t *testing.T) {
	v := Vector{{0, 0.1}, {3, -0.2}, {8, 0.3}, {17, 0.7}}
	cos, _, err := Cosine(v, v)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if cos != 1 || math.Float64bits(cos) != math.Float64bits(1) {
		t.Fatalf("cos = %v (bits %x), want exactly 1", cos, math.Float64bits(cos))
	}
}

func TestCosineZeroNormEmpty(t *testing.T) {
	a := Vector{}
	b := Vector{{0, 1}}
	_, _, err := Cosine(a, b)
	if !errors.Is(err, ErrZeroNorm) {
		t.Fatalf("err = %v, want ErrZeroNorm", err)
	}
	if ve := err.(*Error); ve.Vector != 0 {
		t.Fatalf("vector = %d, want 0", ve.Vector)
	}
}

func TestCosineZeroNormAllZeroSecond(t *testing.T) {
	a := Vector{{0, 1}}
	b := Vector{{0, 0}, {5, 0}}
	_, _, err := Cosine(a, b)
	if !errors.Is(err, ErrZeroNorm) {
		t.Fatalf("err = %v, want ErrZeroNorm", err)
	}
	if ve := err.(*Error); ve.Vector != 1 {
		t.Fatalf("vector = %d, want 1", ve.Vector)
	}
}

func TestCosineKnownValues(t *testing.T) {
	a := Vector{{0, 1}, {1, 2}}
	b := Vector{{0, 2}, {1, 1}}
	cos, _, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if want := 4.0 / (math.Sqrt(5) * math.Sqrt(5)); cos != want {
		t.Fatalf("cos = %v, want %v", cos, want)
	}
	orth := Vector{{0, 1}}
	orth2 := Vector{{1, 1}}
	cos, _, err = Cosine(orth, orth2)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if cos != 0 {
		t.Fatalf("orthogonal cos = %v, want 0", cos)
	}
}

// Random vectors must always produce results inside [-1, 1].
func TestCosineClampedToUnitRange(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 500; trial++ {
		a := randomVector(rng, 8)
		b := randomVector(rng, 8)
		cos, _, err := Cosine(a, b)
		if err != nil {
			t.Fatalf("Cosine: %v", err)
		}
		if cos < -1 || cos > 1 {
			t.Fatalf("trial %d: cos = %v outside [-1, 1]", trial, cos)
		}
	}
}

func randomVector(rng *rand.Rand, n int) Vector {
	v := make(Vector, n)
	for i := range v {
		v[i] = Element{Index: uint32(i), Value: rng.NormFloat64()}
	}
	return v
}

func TestCosineInfiniteValue(t *testing.T) {
	a := Vector{{0, math.Inf(1)}}
	b := Vector{{0, 1}}
	_, _, err := Cosine(a, b)
	if !errors.Is(err, ErrNonFinite) {
		t.Fatalf("err = %v, want ErrNonFinite", err)
	}
}

func TestDotInfiniteAndNaNResults(t *testing.T) {
	inf := Vector{{0, math.Inf(1)}}
	one := Vector{{0, 1}}
	if _, _, err := Dot(inf, one); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("inf dot err = %v, want ErrNonFinite", err)
	}
	mixed := Vector{{0, math.Inf(1)}, {1, math.Inf(-1)}}
	both := Vector{{0, 1}, {1, 1}}
	if _, _, err := Dot(mixed, both); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("inf+-inf dot err = %v, want ErrNonFinite", err)
	}
}
