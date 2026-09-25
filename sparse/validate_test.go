package sparse

import (
	"errors"
	"math"
	"testing"
)

func TestNotStrictlyAscendingWithLocation(t *testing.T) {
	cases := []struct {
		name   string
		a, b   Vector
		vec    int
		pos    int
		target error
	}{
		{
			name:   "equal in vector 1",
			a:      Vector{{1, 1}, {1, 2}},
			b:      Vector{{1, 1}},
			vec:    VectorA,
			pos:    1,
			target: ErrNotSorted,
		},
		{
			name:   "decreasing in vector 2",
			a:      Vector{{1, 1}},
			b:      Vector{{2, 1}, {0, 2}},
			vec:    VectorB,
			pos:    1,
			target: ErrNotSorted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Dot(tc.a, tc.b)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if !errors.Is(err, tc.target) || ve.Vector != tc.vec || ve.Pos != tc.pos {
				t.Fatalf("got %v (vec=%d pos=%d)", err, ve.Vector, ve.Pos)
			}
		})
	}
}

func TestNaNWithLocation(t *testing.T) {
	a := Vector{{0, 1}, {3, math.NaN()}, {9, 2}}
	b := Vector{{0, 1}}
	_, err := Dot(a, b)
	var ve *ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, ErrNaN) ||
		ve.Vector != VectorA || ve.Pos != 1 {
		t.Fatalf("got %v, want ErrNaN vector 1 position 1", err)
	}

	bad := Vector{{5, math.NaN()}}
	if _, err := Dot(b, bad); !errors.Is(err, ErrNaN) {
		t.Fatalf("vector 2 NaN: got %v", err)
	}
}

func TestInfinityValuesAreRejected(t *testing.T) {
	a := Vector{{0, 1}}
	b := Vector{{0, math.Inf(1)}}
	if _, err := Dot(a, b); !errors.Is(err, ErrInf) {
		t.Fatalf("+Inf: got %v, want ErrInf", err)
	}
	b = Vector{{0, math.Inf(-1)}}
	if _, err := Dot(a, b); !errors.Is(err, ErrInf) {
		t.Fatalf("-Inf: got %v, want ErrInf", err)
	}
}

func TestInfinityProductRejected(t *testing.T) {
	// 有限元素但相乘溢出为 +Inf：不得把 Inf/NaN 交给调用方。
	a := Vector{{0, math.MaxFloat64}}
	b := Vector{{0, math.MaxFloat64}}
	if _, err := Dot(a, b); !errors.Is(err, ErrNonFiniteResult) {
		t.Fatalf("overflow product: got %v, want ErrNonFiniteResult", err)
	}
}

func TestEmptyVectorIsValid(t *testing.T) {
	r, err := Dot(Vector{}, Vector{{0, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Dot != 0 || r.Steps != 0 {
		t.Fatalf("empty dot = %v steps %d", r.Dot, r.Steps)
	}
}

func TestInputsAreNotModified(t *testing.T) {
	a := Vector{{0, 2}, {1, 3}}
	b := Vector{{1, 4}, {2, 5}}
	a0, b0 := append(Vector(nil), a...), append(Vector(nil), b...)
	if _, err := Dot(a, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Cosine(a, b); err != nil {
		t.Fatal(err)
	}
	for i := range a0 {
		if a[i] != a0[i] {
			t.Fatalf("a mutated: %v != %v", a, a0)
		}
	}
	for i := range b0 {
		if b[i] != b0[i] {
			t.Fatalf("b mutated: %v != %v", b, b0)
		}
	}
}
