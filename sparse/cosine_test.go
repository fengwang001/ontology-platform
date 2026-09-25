package sparse

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestZeroNormVectorsRejected(t *testing.T) {
	cases := []struct {
		name string
		a, b Vector
	}{
		{"empty a", Vector{}, Vector{{0, 1}}},
		{"all explicit zeros", Vector{{0, 0}, {1, 0}}, Vector{{0, 1}}},
		{"empty b", Vector{{0, 1}}, Vector{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _, err := Cosine(tc.a, tc.b)
			if !errors.Is(err, ErrZeroNorm) {
				t.Fatalf("got %v, want ErrZeroNorm", err)
			}
			if math.IsNaN(v) || v != 0 {
				t.Fatalf("value must not be NaN/garbage, got %v", v)
			}
		})
	}
}

func TestIdenticalVectorsExactlyOne(t *testing.T) {
	a := Vector{{0, 3}, {4, -1.5}, {9, 2.25}, {100, 7}}
	c, _, err := Cosine(a, append(Vector(nil), a...))
	if err != nil {
		t.Fatal(err)
	}
	bits := math.Float64bits(c)
	if bits != math.Float64bits(1.0) {
		t.Fatalf("identical cosine = %b (%.17g), want exact 1.0", bits, c)
	}
}

func TestCosineValuesAndClamping(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		mk := func() Vector {
			n := 1 + rng.Intn(6)
			used := map[uint32]bool{}
			var v Vector
			for len(v) < n {
				idx := uint32(rng.Intn(12))
				if used[idx] {
					continue
				}
				used[idx] = true
				v = append(v, Elem{idx, rng.NormFloat64()})
			}
			sort.Slice(v, func(i, j int) bool { return v[i].Index < v[j].Index })
			return v
		}
		c, _, err := Cosine(mk(), mk())
		if err != nil {
			t.Fatal(err)
		}
		if c < -1 || c > 1 || math.IsNaN(c) {
			t.Fatalf("trial %d: cosine out of range: %v", trial, c)
		}
	}

	// 已知向量对：(3,4)/(-3,-4) → -1（反向，数值恰好可表示）。
	neg, _, err := Cosine(Vector{{0, 3}, {1, 4}}, Vector{{0, -3}, {1, -4}})
	if err != nil || neg != -1 {
		t.Fatalf("opposite: %v, %v", neg, err)
	}
}

func TestRepeatableBitIdentical(t *testing.T) {
	a := Vector{{0, 1.1}, {2, 2.2}, {5, -3.3}}
	b := Vector{{0, 4.4}, {2, -5.5}, {9, 6.6}}
	first, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	c1, _, err := Cosine(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		r, err := Dot(a, b)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(r.Dot) != math.Float64bits(first.Dot) ||
			r.Steps != first.Steps || r.ExplicitZeros != first.ExplicitZeros {
			t.Fatalf("iteration %d not bit identical: %+v vs %+v", i, r, first)
		}
		ci, _, err := Cosine(a, b)
		if err != nil || math.Float64bits(ci) != math.Float64bits(c1) {
			t.Fatalf("cosine iteration %d: %v %v", i, ci, err)
		}
	}
}
