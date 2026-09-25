package sparse

import (
	"math"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// naiveDenseDot 把稀疏向量展开到一个（小规模）稠密切片里朴素累加。
// 仅用于测试对拍，生产代码绝不使用。
func naiveDenseDot(a, b Vector, maxDim int) float64 {
	da := make([]float64, maxDim)
	db := make([]float64, maxDim)
	for _, e := range a {
		da[e.Index] = e.Value
	}
	for _, e := range b {
		db[e.Index] = e.Value
	}
	var sum float64
	for i := range da {
		sum += da[i] * db[i]
	}
	return sum
}

// bigDot 用 math/big 高精度（200 位）计算点积参考值。
func bigDot(t *testing.T, a, b Vector) *big.Float {
	t.Helper()
	const prec = 200
	terms := make(map[uint32]*big.Float)
	for _, x := range a {
		for _, y := range b {
			if x.Index == y.Index {
				fa := new(big.Float).SetPrec(prec).SetFloat64(x.Value)
				fb := new(big.Float).SetPrec(prec).SetFloat64(y.Value)
				p := new(big.Float).SetPrec(prec).Mul(fa, fb)
				if old, ok := terms[x.Index]; ok {
					p.Add(old, p)
				}
				terms[x.Index] = p
			}
		}
	}
	sum := new(big.Float).SetPrec(prec)
	for _, p := range terms {
		sum.Add(sum, p)
	}
	return sum
}

func TestBillionIndexStepsAreSingleDigit(t *testing.T) {
	a := Vector{{0, 1}, {1_000_000_000 / 2, 2}, {1_000_000_000, 3}}
	b := Vector{{1, 4}, {1_000_000_000/2 + 1, 5}, {1_000_000_000, 6}}
	r, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps >= 10 {
		t.Fatalf("steps = %d, want single digit (no dense expansion)", r.Steps)
	}
	if r.Dot != 3*6 {
		t.Fatalf("dot = %v, want %v", r.Dot, 18.0)
	}
}

func TestDotMatchesNaiveDense(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		dim := 1 + rng.Intn(30)
		mk := func() Vector {
			used := map[uint32]bool{}
			n := rng.Intn(dim + 1)
			var v Vector
			for len(v) < n {
				idx := uint32(rng.Intn(dim))
				if used[idx] {
					continue
				}
				used[idx] = true
				v = append(v, Elem{idx, float64(rng.Intn(2001) - 1000)})
			}
			sort.Slice(v, func(i, j int) bool { return v[i].Index < v[j].Index })
			return v
		}
		a, b := mk(), mk()
		r, err := Dot(a, b)
		if err != nil {
			t.Fatal(err)
		}
		want := naiveDenseDot(a, b, dim)
		if math.Float64bits(r.Dot) != math.Float64bits(want) {
			t.Fatalf("trial %d: dot %b != naive %b", trial, r.Dot, want)
		}
	}
}

func TestShuffleResortIsBitIdentical(t *testing.T) {
	a := Vector{{1, 3}, {4, -2}, {7, 5}, {10, 1}, {13, 7}}
	b := Vector{{2, 1}, {4, 6}, {9, -3}, {10, 4}}
	base, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		x := append(Vector(nil), a...)
		rng.Shuffle(len(x), func(i, j int) { x[i], x[j] = x[j], x[i] })
		sort.Slice(x, func(i, j int) bool { return x[i].Index < x[j].Index })
		got, err := Dot(x, b)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(got.Dot) != math.Float64bits(base.Dot) {
			t.Fatalf("trial %d: %b != %b", trial, got.Dot, base.Dot)
		}
	}
}

func TestExplicitZerosCountedAndDoNotAffectDot(t *testing.T) {
	with := Vector{{0, 2}, {1, 0}, {5, 0}, {9, 4}}
	without := Vector{{0, 2}, {9, 4}}
	b := Vector{{0, 3}, {1, 9}, {5, 1}, {9, -1}}
	r1, err := Dot(with, b)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Dot(without, b)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ExplicitZeros != 2 {
		t.Fatalf("explicit zeros = %d, want 2", r1.ExplicitZeros)
	}
	if math.Float64bits(r1.Dot) != math.Float64bits(r2.Dot) || r1.Dot != 2 {
		t.Fatalf("zeros affected dot: %v vs %v", r1.Dot, r2.Dot)
	}
}

func TestBigMagnitudeRelativeError(t *testing.T) {
	// 1e16 与 1 同时出现的项；Kahan 补偿求和保住低位。
	a := Vector{{0, 1e16}, {1, 1}, {2, 1}}
	b := Vector{{0, 1}, {1, 1}, {2, 1}}
	r, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := bigDot(t, a, b).Float64()
	rel := math.Abs(r.Dot-ref) / math.Abs(ref)
	if rel > 1e-15 {
		t.Fatalf("relative error %g > 1e-15 (got %v ref %v)", rel, r.Dot, ref)
	}
	if r.Dot != 1e16+2 { // Kahan 恢复了朴素累加丢失的两个 1
		t.Fatalf("got %v, want %v", r.Dot, 1e16+2)
	}
}
