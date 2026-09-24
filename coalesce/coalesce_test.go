package coalesce

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"ontology/rangespec"
)

func rng(start, end int64) rangespec.Spec { return rangespec.Spec{Start: start, End: end} }
func suffix(n int64) rangespec.Spec       { return rangespec.Spec{Start: -1, Suffix: n} }

func TestNormalizeClipAndMerge(t *testing.T) {
	cases := []struct {
		name  string
		specs []rangespec.Spec
		total int64
		want  []Range
	}{
		{"a-b 终点越界裁剪", []rangespec.Spec{rng(2, 999)}, 10, []Range{{2, 9}}},
		{"a- 到末尾", []rangespec.Spec{rng(3, -1)}, 10, []Range{{3, 9}}},
		{"-n 取末尾", []rangespec.Spec{suffix(4)}, 10, []Range{{6, 9}}},
		{"-n 大于总长取全部", []rangespec.Spec{suffix(100)}, 10, []Range{{0, 9}}},
		{"重叠合并", []rangespec.Spec{rng(0, 5), rng(3, 8)}, 10, []Range{{0, 8}}},
		{"相邻合并", []rangespec.Spec{rng(0, 4), rng(5, 9)}, 10, []Range{{0, 9}}},
		{"排序后合并", []rangespec.Spec{rng(6, 7), rng(0, 1), rng(2, 3)}, 10, []Range{{0, 3}, {6, 7}}},
		{"包含合并", []rangespec.Spec{rng(0, 9), rng(2, 4)}, 10, []Range{{0, 9}}},
		{"间隔不合并", []rangespec.Spec{rng(0, 1), rng(3, 4)}, 10, []Range{{0, 1}, {3, 4}}},
		{"部分不可满足被丢弃", []rangespec.Spec{rng(50, 60), rng(1, 2)}, 10, []Range{{1, 2}}},
	}
	for _, c := range cases {
		got, err := Normalize(c.specs, c.total)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		for i := 1; i < len(got); i++ { // 结果互不重叠、互不相邻
			if got[i].Start <= got[i-1].End+1 {
				t.Errorf("%s: result ranges overlap or adjacent: %v", c.name, got)
			}
		}
	}
}

func TestNormalizeUnsatisfiable(t *testing.T) {
	cases := []struct {
		name  string
		specs []rangespec.Spec
		total int64
	}{
		{"-0 不可满足", []rangespec.Spec{suffix(0)}, 42},
		{"起点越过末尾", []rangespec.Spec{rng(10, 20)}, 10},
		{"b 小于 a", []rangespec.Spec{rng(5, 3)}, 10},
		{"空资源", []rangespec.Spec{rng(0, 0)}, 0},
	}
	for _, c := range cases {
		_, err := Normalize(c.specs, c.total)
		var unsat *rangespec.UnsatisfiableError
		if !errors.As(err, &unsat) {
			t.Errorf("%s: want UnsatisfiableError, got %v", c.name, err)
			continue
		}
		if unsat.Total != c.total {
			t.Errorf("%s: Total = %d, want %d", c.name, unsat.Total, c.total)
		}
		var syn *rangespec.SyntaxError
		if errors.As(err, &syn) {
			t.Errorf("%s: unsatisfiable must not match SyntaxError", c.name)
		}
	}
}

// expand 把区间列表展开成字节下标集合，用于穷举对照。
func expand(rs []Range) map[int64]bool {
	set := map[int64]bool{}
	for _, r := range rs {
		for i := r.Start; i <= r.End; i++ {
			set[i] = true
		}
	}
	return set
}

func TestNormalizePreservesByteSet(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 2000; trial++ {
		total := int64(1 + r.Intn(40))
		var specs []rangespec.Spec
		var before []Range
		for i := 0; i < 1+r.Intn(8); i++ {
			s := rangespec.Spec{Start: int64(r.Intn(int(total) + 5))}
			switch r.Intn(3) {
			case 0:
				s.End = s.Start + int64(r.Intn(int(total)))
			case 1:
				s.End = -1
			case 2:
				s.Start, s.Suffix = -1, int64(r.Intn(int(total)+2))
			}
			specs = append(specs, s)
			if one, ok := resolve(s, total); ok {
				before = append(before, one)
			}
		}
		got, err := Normalize(specs, total)
		if len(before) == 0 {
			if err == nil {
				t.Fatalf("trial %d: all unsatisfiable but got %v", trial, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: unexpected error %v", trial, err)
		}
		if !reflect.DeepEqual(expand(before), expand(got)) {
			t.Fatalf("trial %d: byte set changed: specs=%v before=%v after=%v", trial, specs, before, got)
		}
	}
}

func TestCompareCountIsNLogN(t *testing.T) {
	counts := map[int]int64{}
	for _, n := range []int{100, 10000} {
		r := rand.New(rand.NewSource(int64(n)))
		specs := make([]rangespec.Spec, n)
		for i := range specs {
			specs[i] = rangespec.Spec{Start: int64(r.Intn(n * 2)), End: int64(r.Intn(n * 4))}
		}
		ResetCompareCount()
		if _, err := Normalize(specs, int64(n*4)); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		counts[n] = CompareCount()
	}
	c1, c2 := counts[100], counts[10000]
	ratio := float64(c2) / float64(c1)
	nlognRatio := (10000 * math.Log2(10000)) / (100 * math.Log2(100)) // ≈200
	t.Logf("n=100: %d comparisons; n=10000: %d; ratio %.1f (n·log n ratio %.1f)", c1, c2, ratio, nlognRatio)
	if ratio > 2*nlognRatio {
		t.Fatalf("comparison growth %.1f exceeds 2x n·log n bound %.1f (O(n^2) would be ~10000)", ratio, 2*nlognRatio)
	}
}
