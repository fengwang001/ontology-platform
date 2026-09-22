package sparse

import (
	"math"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// bigDot computes the dot product at 256-bit precision as reference.
func bigDot(a, b Vector) *big.Float {
	vals := map[uint32]*big.Float{}
	for _, e := range a {
		vals[e.Index] = new(big.Float).SetPrec(256).SetFloat64(e.Value)
	}
	sum := new(big.Float).SetPrec(256)
	for _, e := range b {
		if v, ok := vals[e.Index]; ok {
			term := new(big.Float).SetPrec(256).Mul(v, big.NewFloat(e.Value))
			sum.Add(sum, term)
		}
	}
	return sum
}

func relErr(got float64, ref *big.Float) float64 {
	r, _ := ref.Float64()
	if r == 0 {
		return math.Abs(got)
	}
	return math.Abs(got-r) / math.Abs(r)
}

func TestDotMixedMagnitudesVsBig(t *testing.T) {
	cases := []struct {
		name string
		a, b Vector
	}{
		{
			name: "1e16 plus 1 minus 1e16",
			a:    Vector{{0, 1e16}, {1, 1}, {2, -1e16}},
			b:    Vector{{0, 1}, {1, 1}, {2, 1}},
		},
		{
			name: "tiny term next to huge term",
			a:    Vector{{0, 1e16}, {1, 1}},
			b:    Vector{{0, 1}, {1, 1}},
		},
		{
			name: "many small terms against one huge",
			a:    Vector{{0, 1e15}, {1, 0.5}, {2, 0.25}, {3, 0.125}, {4, -1e15}},
			b:    Vector{{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Dot(tc.a, tc.b)
			if err != nil {
				t.Fatalf("Dot: %v", err)
			}
			if e := relErr(got, bigDot(tc.a, tc.b)); e > 1e-15 {
				t.Fatalf("relative error %g > 1e-15 (got %v)", e, got)
			}
		})
	}
}

func TestDotShuffledThenResortedBitwise(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a := Vector{{0, 1e16}, {1, 1}, {2, -3.5}, {3, 1e-8}, {4, 7}, {5, -1e16}}
	b := Vector{{0, 2}, {1, 3}, {2, 0.5}, {3, 1e8}, {4, -11}, {5, 1}}

	base, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	for trial := 0; trial < 50; trial++ {
		shuffled := make(Vector, len(b))
		copy(shuffled, b)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		sort.Slice(shuffled, func(i, j int) bool {
			return shuffled[i].Index < shuffled[j].Index
		})
		got, _, err := Dot(a, shuffled)
		if err != nil {
			t.Fatalf("Dot: %v", err)
		}
		if math.Float64bits(got) != math.Float64bits(base) {
			t.Fatalf("trial %d: %v != %v bitwise", trial, got, base)
		}
	}
}
