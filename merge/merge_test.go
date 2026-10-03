package merge

import (
	"errors"
	"math"
	"slices"
	"testing"

	"ontology/schema"
)

func mkHist(name string, bounds, counts []int64, sum int64) schema.Hist {
	return schema.Hist{Name: name, Bounds: bounds, Counts: counts, Sum: sum}
}

// 规格书示例：交集 [20,100]，a 的前两桶上移并入 ≤20，(20,50] 上移并入 ≤100。
func TestMergeExample(t *testing.T) {
	a := mkHist("lat", []int64{10, 20, 50, 100}, []int64{3, 2, 4, 1, 0}, 500)
	b := mkHist("lat", []int64{20, 100}, []int64{1, 8, 3}, 700)
	got, err := Merge(&a, &b)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入 a=%v b=%v -> 输出 %v（依据：交集 [20,100]，源桶上移并桶）", a, b, got)
	if !slices.Equal(got.Bounds, []int64{20, 100}) || !slices.Equal(got.Counts, []int64{6, 13, 3}) || got.Sum != 1200 {
		t.Fatalf("got %+v", got)
	}
	// 输入保持不变
	if !slices.Equal(a.Counts, []int64{3, 2, 4, 1, 0}) || a.Sum != 500 {
		t.Fatalf("input mutated: %+v", a)
	}
}

func TestMergeErrorOrder(t *testing.T) {
	valid := mkHist("x", []int64{10}, []int64{1, 0}, 5)
	bad := mkHist("x", []int64{10}, []int64{1}, 5) // Counts 长度错
	other := mkHist("y", []int64{20}, []int64{1, 0}, 5)
	if _, err := Merge(&bad, &other); !errors.Is(err, schema.ErrInvalid) {
		t.Fatalf("invalid 优先于名字: %v", err)
	}
	disjoint := mkHist("y", []int64{20, 30}, []int64{1, 0, 0}, 5) // 与 valid 名字不同且无交集
	if _, err := Merge(&valid, &disjoint); !errors.Is(err, schema.ErrNameMismatch) {
		t.Fatalf("名字优先于不兼容: %v", err)
	}
	// 不兼容优先于溢出：无公共边界且计数若合并会溢出
	big := mkHist("x", []int64{10}, []int64{math.MaxInt64, 0}, 1)
	big2 := mkHist("x", []int64{20}, []int64{math.MaxInt64, 0}, 1)
	if _, err := Merge(&big, &big2); !errors.Is(err, schema.ErrIncompatible) {
		t.Fatalf("不兼容优先于溢出: %v", err)
	}
	small := mkHist("x", []int64{10}, []int64{1, 0}, 1)
	if _, err := Merge(&big, &small); !errors.Is(err, schema.ErrOverflow) {
		t.Fatalf("溢出: %v", err)
	}
	if big.Counts[0] != math.MaxInt64 || big.Sum != 1 { // 溢出时输入不变
		t.Fatalf("input mutated: %+v", big)
	}
}

func TestMergeCompareCounter(t *testing.T) {
	mk := func(step, n int) []int64 {
		b := make([]int64, n)
		for i := range b {
			b[i] = int64((i + 1) * step)
		}
		return b
	}
	// 64 对 64：交错边界（最坏情况）
	a := mkHist("c", mk(2, 64), make([]int64, 65), 0) // 2,4,...,128
	b := mkHist("c", odd(64), make([]int64, 65), 0)   // 1,3,...,127
	Merge(&a, &b)                                     // 无交集，报不兼容，但计数器已记录
	if got := mergeCompares.Load(); got > 128 {
		t.Fatalf("64v64 compares=%d > 128", got)
	}
	t.Logf("64v64 比较次数=%d（上限 128）", mergeCompares.Load())
	// 3 对 3
	c := mkHist("c", []int64{1, 2, 3}, make([]int64, 4), 0)
	d := mkHist("c", []int64{2, 3, 4}, make([]int64, 4), 0)
	if _, err := Merge(&c, &d); err != nil {
		t.Fatal(err)
	}
	if got := mergeCompares.Load(); got > 6 {
		t.Fatalf("3v3 compares=%d > 6", got)
	}
	t.Logf("3v3 比较次数=%d（上限 6）", mergeCompares.Load())
}

func odd(n int) []int64 {
	b := make([]int64, n)
	for i := range b {
		b[i] = int64(2*i + 1)
	}
	return b
}

func TestIntersectionEqualsSmallerSide(t *testing.T) {
	a := mkHist("s", []int64{20, 100}, []int64{5, 0, 3}, 100)
	b := mkHist("s", []int64{10, 20, 50, 100}, []int64{1, 4, 2, 1, 0}, 200)
	got, err := Merge(&a, &b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Bounds, []int64{20, 100}) {
		t.Fatalf("交集应等于较小一侧: %v", got.Bounds)
	}
	// a 的 ≤20 桶与 b 的 ≤10、(10,20] 合并: 5+1+4=10；≤100: 0+2+1=3；溢出 3+0=3
	if !slices.Equal(got.Counts, []int64{10, 3, 3}) || got.Sum != 300 {
		t.Fatalf("got %+v", got)
	}
}

// 版本链 v1⊃v2⊂v3⊃v4：相邻相容，但 v1 与 v4 无公共边界，合并不兼容。
func TestVersionChainEndsIncompatible(t *testing.T) {
	reg := schema.NewRegistry()
	steps := [][]int64{{10, 20, 50, 100}, {20, 100}, {20, 60, 100}, {60}}
	for i, b := range steps {
		if v, err := reg.Register("chain", b); err != nil || v != i+1 {
			t.Fatalf("register v%d: (%d, %v)", i+1, v, err)
		}
	}
	h1 := mkHist("chain", steps[0], []int64{1, 1, 1, 1, 0}, 100)
	h4 := mkHist("chain", steps[3], []int64{2, 0}, 50)
	if _, err := Merge(&h1, &h4); !errors.Is(err, schema.ErrIncompatible) {
		t.Fatalf("v1 与 v4 应不兼容: %v", err)
	}
	h2 := mkHist("chain", steps[1], []int64{1, 1, 0}, 60)
	if _, err := Merge(&h1, &h2); err != nil { // 相邻版本可合并
		t.Fatalf("v1 与 v2 应可合并: %v", err)
	}
}

func TestMergeAllPermutationInvariant(t *testing.T) {
	hs := []schema.Hist{
		mkHist("m", []int64{10, 20, 50, 100}, []int64{3, 2, 4, 1, 0}, 500),
		mkHist("m", []int64{20, 100}, []int64{1, 8, 3}, 700),
		mkHist("m", []int64{20, 60, 100, 200}, []int64{0, 5, 5, 0, 1}, 800),
	}
	want, err := MergeAll(hs)
	if err != nil {
		t.Fatal(err)
	}
	perm := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, p := range perm {
		got, err := MergeAll([]schema.Hist{hs[p[0]], hs[p[1]], hs[p[2]]})
		if err != nil || !slices.Equal(got.Bounds, want.Bounds) || !slices.Equal(got.Counts, want.Counts) || got.Sum != want.Sum {
			t.Fatalf("perm %v: got %+v want %+v", p, got, want)
		}
		// 结合方式无关：逐个 Merge 的结果相同
		ab, e1 := Merge(&hs[p[0]], &hs[p[1]])
		abc, e2 := Merge(&ab, &hs[p[2]])
		if e1 != nil || e2 != nil || !slices.Equal(abc.Counts, want.Counts) || abc.Sum != want.Sum {
			t.Fatalf("fold perm %v: %+v vs %+v (errs %v %v)", p, abc, want, e1, e2)
		}
	}
	t.Logf("输入 %v -> MergeAll 输出 %v（依据：全体边界交集+逐桶守恒，与排列/结合无关）", hs, want)
	if _, err := MergeAll(nil); !errors.Is(err, schema.ErrInvalid) {
		t.Fatalf("空列表: %v", err)
	}
	mixed := []schema.Hist{hs[0], mkHist("other", []int64{20, 100}, []int64{1, 0, 0}, 1)}
	if _, err := MergeAll(mixed); !errors.Is(err, schema.ErrNameMismatch) {
		t.Fatalf("异名: %v", err)
	}
}
