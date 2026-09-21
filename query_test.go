package ontology

import (
	"math/bits"
	"testing"
)

// 上万个区间下单点查询的检查数必须是对数量级。
func TestCountAtChecksAreLogarithmic(t *testing.T) {
	const n = 20000
	c := New()
	for i := 0; i < n; i++ {
		mustAdd(t, c, int64(i), int64(i+1000))
	}
	// 端点表至多 2n 个端点；二分检查数上界为 ceil(log2(2n))+1。
	maxChecks := bits.Len(uint(2*n)) + 1
	for _, p := range []int64{0, 500, 9999, 19999, 21000, -1} {
		_, checked := c.CountAt(p)
		if checked > maxChecks {
			t.Fatalf("CountAt(%d) checked %d endpoints, want <= %d (log scale)", p, checked, maxChecks)
		}
		if checked > 64 {
			t.Fatalf("CountAt(%d) checked %d endpoints, not logarithmic", p, checked)
		}
	}
	// 抽查一个点的计数正确性：p 被 [p-999, p] 内开始的区间覆盖。
	got, _ := c.CountAt(15000)
	if got != 1000 {
		t.Fatalf("CountAt(15000) = %d, want 1000", got)
	}
}

// 重复查询不重复物化：第二次查询的检查数仍是对数量级（不扫描）。
func TestCountAtNoRescanAfterMaterialize(t *testing.T) {
	c := New()
	for i := 0; i < 5000; i++ {
		mustAdd(t, c, int64(2*i), int64(2*i+2))
	}
	for round := 0; round < 3; round++ {
		_, checked := c.CountAt(100)
		if checked > 32 {
			t.Fatalf("round %d: checked %d, want <= 32", round, checked)
		}
	}
}

// MaxCoverage 返回最大层数及一个达到该层数的起始位置。
func TestMaxCoverage(t *testing.T) {
	c := New()
	if _, _, ok := c.MaxCoverage(); ok {
		t.Fatal("MaxCoverage on empty counter: ok = true, want false")
	}
	mustAdd(t, c, 0, 10)
	mustAdd(t, c, 5, 15)
	mustAdd(t, c, 7, 9)
	count, start, ok := c.MaxCoverage()
	if !ok {
		t.Fatal("MaxCoverage: ok = false, want true")
	}
	if count != 3 {
		t.Fatalf("MaxCoverage count = %d, want 3", count)
	}
	if got, _ := c.CountAt(start); got != count {
		t.Fatalf("CountAt(start=%d) = %d, want %d", start, got, count)
	}
	if start != 7 {
		t.Fatalf("MaxCoverage start = %d, want 7 (first position reaching max)", start)
	}
	// 移除一层后最大值下降。
	mustRemove(t, c, 7, 9)
	count, start, ok = c.MaxCoverage()
	if !ok || count != 2 || start != 5 {
		t.Fatalf("after remove: MaxCoverage = (%d, %d, %v), want (2, 5, true)", count, start, ok)
	}
	// 全部移除后回到无覆盖。
	mustRemove(t, c, 0, 10)
	mustRemove(t, c, 5, 15)
	if _, _, ok := c.MaxCoverage(); ok {
		t.Fatal("MaxCoverage after full removal: ok = true, want false")
	}
}

// MaxCoverage 与 Segments 互相印证：最大层数等于视图中的最大 Count。
func TestMaxCoverageMatchesSegments(t *testing.T) {
	c := New()
	for i := 0; i < 500; i++ {
		mustAdd(t, c, int64(i%50), int64(i%50+100))
	}
	count, start, ok := c.MaxCoverage()
	if !ok {
		t.Fatal("MaxCoverage: ok = false")
	}
	var maxSeg int64
	for _, s := range c.Segments() {
		if s.Count > maxSeg {
			maxSeg = s.Count
		}
	}
	if count != maxSeg {
		t.Fatalf("MaxCoverage = %d, max segment count = %d", count, maxSeg)
	}
	if got, _ := c.CountAt(start); got != count {
		t.Fatalf("CountAt(start) = %d, want %d", got, count)
	}
}

// 不同区间重数混合时的计数抽查。
func TestMixedMultiplicityCounts(t *testing.T) {
	c := New()
	for i := 0; i < 4; i++ {
		mustAdd(t, c, 0, 100)
	}
	for i := 0; i < 2; i++ {
		mustAdd(t, c, 50, 60)
	}
	mustRemove(t, c, 0, 100)
	cases := []struct {
		p    int64
		want int64
	}{
		{0, 3}, {49, 3}, {50, 5}, {55, 5}, {59, 5}, {60, 3}, {99, 3}, {100, 0},
	}
	for _, tc := range cases {
		if got := countAt(t, c, tc.p); got != tc.want {
			t.Fatalf("CountAt(%d) = %d, want %d", tc.p, got, tc.want)
		}
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 基准：确认查询路径不随区间数线性退化（供人工观察，不参与断言）。
func BenchmarkCountAt(b *testing.B) {
	c := New()
	for i := 0; i < 20000; i++ {
		if err := c.Add(int64(i), int64(i+1000)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.CountAt(int64(i % 20000))
	}
}
