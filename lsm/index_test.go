package lsm

import (
	"fmt"
	"testing"
)

// buildIndex 构造含 n 个互不重叠文件的索引，第 i 个文件为 [4i, 4i+1]。
func buildIndex(n int) *sortedIndex {
	ix := &sortedIndex{}
	for i := 0; i < n; i++ {
		lo := byteKey(4 * i)
		hi := byteKey(4*i + 1)
		ix.insert(FileMeta{ID: uint64(i + 1), Level: 1, Smallest: lo, Largest: hi, Size: 1})
	}
	return ix
}

// byteKey 把非负整数编码为保序的定长字节串。
func byteKey(v int) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

// TestOverlapQueryIsLogarithmic 可验证地证明：为目标层找出与一个区间
// 重叠的文件，其键比较开销随该层文件总数呈对数增长而非线性增长。
// 文件数放大 100 倍（1e3 -> 1e5），比较次数的增长不得超过对数比值允许的界限。
func TestOverlapQueryIsLogarithmic(t *testing.T) {
	const small, large = 1_000, 100_000
	ixSmall, ixLarge := buildIndex(small), buildIndex(large)
	query := func(ix *sortedIndex, n int) int {
		mid := n / 2
		lo := byteKey(4 * mid)
		hi := byteKey(4*mid + 5)
		got := ix.overlap(lo, hi)
		if len(got) != 2 {
			t.Fatalf("expected 2 overlaps, got %d", len(got))
		}
		return ix.lastQueryComparisons()
	}
	cSmall := query(ixSmall, small)
	cLarge := query(ixLarge, large)
	t.Logf("overlap query comparisons: n=%d -> %d, n=%d -> %d", small, cSmall, large, cLarge)
	// 对数上界：二分约 log2(n) 次，加 k 次扫描与 1 次终止比较。
	// n 放大 100 倍时，log2 仅增加约 13.3；若开销为线性，比较次数应放大 100 倍。
	if cLarge > cSmall+20 {
		t.Fatalf("comparisons grew too fast: %d -> %d for 100x files; want O(log n)", cSmall, cLarge)
	}
	if cLarge > 40 {
		t.Fatalf("absolute comparisons %d exceed logarithmic bound for n=%d", cLarge, large)
	}
}

// TestOverlapQueryCorrectness 对重叠查询（含端点相等）做穷举校验。
func TestOverlapQueryCorrectness(t *testing.T) {
	ix := &sortedIndex{}
	// 端点相接的文件链：[0,2],[2,4],[4,6],[6,8]
	for i := 0; i < 4; i++ {
		lo := byteKey(2 * i)
		hi := byteKey(2*i + 2)
		ix.insert(FileMeta{ID: uint64(i + 1), Level: 1, Smallest: lo, Largest: hi, Size: 1})
	}
	cases := []struct {
		lo, hi int
		want   int
	}{
		{1, 1, 1}, // 落在 [0,2] 内
		{2, 2, 2}, // 端点 2 同时属于 [0,2] 与 [2,4]
		{3, 5, 2}, // 跨 [2,4]、[4,6]
		{8, 9, 1}, // 端点 8 属于 [6,8]
		{9, 9, 0}, // 完全在外
		{0, 8, 4}, // 全覆盖
		{2, 6, 4}, // 端点 2 与 6 都算重叠，链式命中全部四个
	}
	for _, c := range cases {
		got := ix.overlap(byteKey(c.lo), byteKey(c.hi))
		if len(got) != c.want {
			t.Fatalf("overlap[%d,%d] = %d files, want %d", c.lo, c.hi, len(got), c.want)
		}
	}
}

// TestWithMinWithMax 边界闭合所需的等值查询。
func TestWithMinWithMax(t *testing.T) {
	ix := &sortedIndex{}
	// 同一键上的单键文件链：[5,5] x 3，以及 [5,9]。
	ix.insert(FileMeta{ID: 1, Level: 1, Smallest: byteKey(5), Largest: byteKey(5), Size: 1})
	ix.insert(FileMeta{ID: 2, Level: 1, Smallest: byteKey(5), Largest: byteKey(5), Size: 1})
	ix.insert(FileMeta{ID: 3, Level: 1, Smallest: byteKey(5), Largest: byteKey(5), Size: 1})
	ix.insert(FileMeta{ID: 4, Level: 1, Smallest: byteKey(5), Largest: byteKey(9), Size: 1})
	if got := ix.withMin(byteKey(5)); len(got) != 4 {
		t.Fatalf("withMin(5) = %d, want 4", len(got))
	}
	if got := ix.withMax(byteKey(5)); len(got) != 3 {
		t.Fatalf("withMax(5) = %d, want 3", len(got))
	}
	if got := ix.withMax(byteKey(9)); len(got) != 1 {
		t.Fatalf("withMax(9) = %d, want 1", len(got))
	}
	if got := ix.withMin(byteKey(6)); len(got) != 0 {
		t.Fatalf("withMin(6) = %d, want 0", len(got))
	}
}

// TestFirstAfter 非零层起点所需的“严格大于”查询。
func TestFirstAfter(t *testing.T) {
	ix := &sortedIndex{}
	for i, lo := range []int{10, 20, 20, 30} {
		ix.insert(FileMeta{ID: uint64(i + 1), Level: 1, Smallest: byteKey(lo), Largest: byteKey(lo + 5), Size: 1})
	}
	f, ok := ix.firstAfter(byteKey(10))
	if !ok || f.Smallest[3] != 20 {
		t.Fatalf("firstAfter(10) = %v, %v", f, ok)
	}
	// 严格大于：min==20 的两个文件都被跳过。
	f, ok = ix.firstAfter(byteKey(20))
	if !ok || f.Smallest[3] != 30 {
		t.Fatalf("firstAfter(20) must skip min==20, got %v, %v", f, ok)
	}
	if _, ok = ix.firstAfter(byteKey(35)); ok {
		t.Fatalf("firstAfter(35) must be empty")
	}
}

// ExampleService 演示完整闭环：登记、选取、安装。
func ExampleService() {
	s, _ := New(Config{NumLevels: 3, L0Trigger: 2, BaseLevelBytes: 100, LevelMultiplier: 10})
	_ = s.AddFile(FileMeta{ID: 1, Level: 0, Smallest: []byte("a"), Largest: []byte("c"), Size: 10})
	_ = s.AddFile(FileMeta{ID: 2, Level: 0, Smallest: []byte("b"), Largest: []byte("d"), Size: 10})
	p, _ := s.Pick()
	fmt.Println(p.Level, p.TargetLevel, p.Kind, p.Score)
	_ = s.Install(p.ID, []FileMeta{{ID: 3, Level: 1, Smallest: []byte("a"), Largest: []byte("d"), Size: 20}})
	f, _ := s.File(3)
	fmt.Println(f.ID, f.Level)
	// Output:
	// 0 1 rewrite 1
	// 3 1
}
