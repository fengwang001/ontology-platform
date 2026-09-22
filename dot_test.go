package ontology

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

// naiveDot 是朴素展开参考实现：用 map 模拟稠密展开，仅用于小规模对拍。
func naiveDot(a, b Vector) float64 {
	dense := make(map[uint32]float64, len(a))
	for _, e := range a {
		dense[e.Index] = e.Value
	}
	var sum float64
	for _, e := range b {
		sum += dense[e.Index] * e.Value
	}
	return sum
}

func TestDotBillionIndexSteps(t *testing.T) {
	a := Vector{{0, 1}, {500_000_000, 2}, {1_000_000_000, 3}}
	b := Vector{{1, 1}, {500_000_000, 4}, {1_000_000_000, 5}}

	got, st, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}
	if st.Steps >= 10 {
		t.Fatalf("推进步数 %d 应为个位数（下标最大到十亿）", st.Steps)
	}
	want := naiveDot(a, b)
	if got != want {
		t.Fatalf("与朴素展开对拍不一致: got %v, want %v", got, want)
	}
	if want != 23 {
		t.Fatalf("参考值异常: %v", want)
	}
}

func TestDotMatchesNaiveOnSmallRandom(t *testing.T) {
	// 确定性伪随机（线性同余），小规模多组对拍。
	seed := uint64(42)
	next := func(n uint32) uint32 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return uint32(seed>>33) % n
	}
	for round := 0; round < 200; round++ {
		var a, b Vector
		idx := uint32(0)
		for i := 0; i < 8; i++ {
			idx += next(5)
			a = append(a, Element{idx, float64(int(next(21)) - 10)})
			idx++
		}
		idx = 0
		for i := 0; i < 8; i++ {
			idx += next(5)
			b = append(b, Element{idx, float64(int(next(21)) - 10)})
			idx++
		}
		got, _, err := Dot(a, b)
		if err != nil {
			t.Fatalf("第 %d 轮 Dot 出错: %v", round, err)
		}
		if want := naiveDot(a, b); got != want {
			t.Fatalf("第 %d 轮对拍不一致: got %v, want %v", round, got, want)
		}
	}
}

func TestDotExplicitZerosCountedAndHarmless(t *testing.T) {
	withZerosA := Vector{{0, 0}, {1, 2}, {7, 0}}
	withZerosB := Vector{{1, 3}, {2, 0}}
	got, st, err := Dot(withZerosA, withZerosB)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}
	if st.ZerosA != 2 || st.ZerosB != 1 || st.ExplicitZeros() != 3 {
		t.Fatalf("显式零计数错误: %+v", st)
	}
	if got != 6 {
		t.Fatalf("显式零影响了点积: got %v, want 6", got)
	}

	cleanA := Vector{{1, 2}}
	cleanB := Vector{{1, 3}}
	want, _, err := Dot(cleanA, cleanB)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}
	if got != want {
		t.Fatalf("去掉显式零后结果变化: got %v, want %v", got, want)
	}
}

func TestDotShuffleResortBitwiseIdentical(t *testing.T) {
	base := Vector{
		{0, 1.5}, {3, -2.25}, {9, 1e10}, {40, 0.75},
		{100, -3.125}, {999, 2.5}, {1 << 20, -1.25},
	}
	other := Vector{
		{0, 2}, {3, 3}, {9, -1e-10}, {40, 4},
		{100, 5}, {999, -6}, {1 << 20, 7},
	}
	want, _, err := Dot(base, other)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}

	// 打乱后重新按下标排序（用新切片，不改原向量），结果必须逐位相同。
	seed := uint64(7)
	for round := 0; round < 50; round++ {
		shuffled := make(Vector, len(base))
		perm := make([]int, len(base))
		for i := range perm {
			perm[i] = i
		}
		for i := len(perm) - 1; i > 0; i-- {
			seed = seed*6364136223846793005 + 1442695040888963407
			j := int(seed>>33) % (i + 1)
			perm[i], perm[j] = perm[j], perm[i]
		}
		for i, p := range perm {
			shuffled[i] = base[p]
		}
		sortByIndex(shuffled)
		got, _, err := Dot(shuffled, other)
		if err != nil {
			t.Fatalf("第 %d 轮 Dot 出错: %v", round, err)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("第 %d 轮结果非逐位一致: got %b, want %b",
				round, math.Float64bits(got), math.Float64bits(want))
		}
	}
}

// sortByIndex 是测试用的插入排序（切片很短）。
func sortByIndex(v Vector) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].Index < v[j-1].Index; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func TestDotMagnitudeDisparityVsBig(t *testing.T) {
	// 量级悬殊：1e16 与 1 混合，朴素从左到右求和会丢掉小项。
	a := Vector{
		{0, 1e16}, {1, 1}, {2, -1e16}, {3, 1},
		{4, 1e16}, {5, -1}, {6, -1e16}, {7, 3},
	}
	b := Vector{
		{0, 1}, {1, 1}, {2, 1}, {3, 1},
		{4, 1}, {5, 1}, {6, 1}, {7, 1},
	}
	got, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot 出错: %v", err)
	}

	ref := bigDotRef(a, b)
	rel := relErrBig(got, ref)
	if rel >= 1e-15 {
		t.Fatalf("相对误差 %g 超过 1e-15（got %v）", rel, got)
	}
	// 该用例的精确结果为 4，补偿求和应直接命中。
	if got != 4 {
		t.Fatalf("补偿求和未命中精确值: got %v, want 4", got)
	}
}

// bigDotRef 用 math/big 高精度计算点积参考值。
func bigDotRef(a, b Vector) *big.Float {
	dense := make(map[uint32]float64, len(a))
	for _, e := range a {
		dense[e.Index] = e.Value
	}
	sum := new(big.Float).SetPrec(256)
	for _, e := range b {
		if av, ok := dense[e.Index]; ok {
			p := new(big.Float).SetPrec(256).SetFloat64(av)
			p.Mul(p, new(big.Float).SetPrec(256).SetFloat64(e.Value))
			sum.Add(sum, p)
		}
	}
	return sum
}

// relErrBig 返回 got 相对高精度参考 ref 的相对误差。
func relErrBig(got float64, ref *big.Float) float64 {
	diff := new(big.Float).SetPrec(256).SetFloat64(got)
	diff.Sub(diff, ref)
	diff.Abs(diff)
	den := new(big.Float).SetPrec(256).Abs(ref)
	if den.Sign() == 0 {
		f, _ := diff.Float64()
		return f
	}
	rel := new(big.Float).SetPrec(256).Quo(diff, den)
	f, _ := rel.Float64()
	return f
}

func TestDotNonFiniteError(t *testing.T) {
	cases := []struct {
		name string
		a, b Vector
	}{
		{"正无穷", Vector{{0, math.Inf(1)}}, Vector{{0, 1}}},
		{"负无穷", Vector{{0, math.Inf(-1)}}, Vector{{0, 2}}},
		{"无穷抵消为NaN", Vector{{0, math.Inf(1)}, {1, math.Inf(1)}},
			Vector{{0, 1}, {1, -1}}},
		{"有限值溢出", Vector{{0, 1e308}}, Vector{{0, 1e308}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Dot(c.a, c.b)
			var nf *NonFiniteError
			if !errors.As(err, &nf) {
				t.Fatalf("期望 *NonFiniteError，得到 %v", err)
			}
		})
	}
}

func TestDotEmptyVectors(t *testing.T) {
	got, st, err := Dot(nil, nil)
	if err != nil {
		t.Fatalf("空向量不应出错: %v", err)
	}
	if got != 0 || st.Steps != 0 {
		t.Fatalf("空向量点积应为 0 且步数为 0: got %v, %+v", got, st)
	}
}
