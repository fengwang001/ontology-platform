package ontology

import (
	"errors"
	"math"
	"testing"
)

func mustAdd(t *testing.T, c *Counter, lo, hi int64) {
	t.Helper()
	if err := c.Add(lo, hi); err != nil {
		t.Fatalf("Add(%d, %d): %v", lo, hi, err)
	}
}

func mustRemove(t *testing.T, c *Counter, lo, hi int64) {
	t.Helper()
	if err := c.Remove(lo, hi); err != nil {
		t.Fatalf("Remove(%d, %d): %v", lo, hi, err)
	}
}

func countAt(t *testing.T, c *Counter, p int64) int64 {
	t.Helper()
	n, _ := c.CountAt(p)
	return n
}

// 同一区间添加三次得到三层覆盖；覆盖范围内每一点恰好 +3。
func TestTripleAddGivesThreeLayers(t *testing.T) {
	c := New()
	for i := 0; i < 3; i++ {
		mustAdd(t, c, 10, 20)
	}
	for _, p := range []int64{10, 11, 15, 19} {
		if got := countAt(t, c, p); got != 3 {
			t.Fatalf("CountAt(%d) = %d, want 3", p, got)
		}
	}
	if got := countAt(t, c, 20); got != 0 {
		t.Fatalf("CountAt(20) = %d, want 0", got)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Remove 一次只减一层，减到零才真正消失。
func TestRemoveDecrementsOneLayer(t *testing.T) {
	c := New()
	for i := 0; i < 3; i++ {
		mustAdd(t, c, 10, 20)
	}
	mustRemove(t, c, 10, 20)
	if got := countAt(t, c, 15); got != 2 {
		t.Fatalf("after 1 remove: CountAt(15) = %d, want 2", got)
	}
	mustRemove(t, c, 10, 20)
	if got := countAt(t, c, 15); got != 1 {
		t.Fatalf("after 2 removes: CountAt(15) = %d, want 1", got)
	}
	mustRemove(t, c, 10, 20)
	if got := countAt(t, c, 15); got != 0 {
		t.Fatalf("after 3 removes: CountAt(15) = %d, want 0", got)
	}
	if segs := c.Segments(); len(segs) != 0 {
		t.Fatalf("Segments after full removal = %v, want empty", segs)
	}
}

// 多减必须返回可判定错误，且计数不得变成负数。
func TestOverRemoveReturnsDecidableError(t *testing.T) {
	c := New()
	mustAdd(t, c, 10, 20)
	mustRemove(t, c, 10, 20)
	err := c.Remove(10, 20)
	if !errors.Is(err, ErrNotPresent) {
		t.Fatalf("over-remove err = %v, want ErrNotPresent", err)
	}
	if got := countAt(t, c, 15); got != 0 {
		t.Fatalf("CountAt(15) = %d, want 0 (no negative)", got)
	}
	// 从未添加过的区间同样可判定。
	if err := c.Remove(0, 1); !errors.Is(err, ErrNotPresent) {
		t.Fatalf("remove never-added err = %v, want ErrNotPresent", err)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 端点语义：CountAt(lo) 命中，CountAt(hi) 不命中。
func TestEndpointSemantics(t *testing.T) {
	c := New()
	mustAdd(t, c, 10, 20)
	if got := countAt(t, c, 10); got != 1 {
		t.Fatalf("CountAt(lo=10) = %d, want 1", got)
	}
	if got := countAt(t, c, 20); got != 0 {
		t.Fatalf("CountAt(hi=20) = %d, want 0", got)
	}
	if got := countAt(t, c, 9); got != 0 {
		t.Fatalf("CountAt(9) = %d, want 0", got)
	}
}

// 同一点同时开始与结束：该点计入在此开始的区间，不计入在此结束的
// 区间；结果确定且与添加顺序无关。
func TestSamePointStartAndEnd(t *testing.T) {
	orders := [][]Interval{
		{{0, 5}, {5, 10}},
		{{5, 10}, {0, 5}},
	}
	for _, order := range orders {
		c := New()
		for _, iv := range order {
			mustAdd(t, c, iv.Lo, iv.Hi)
		}
		if got := countAt(t, c, 5); got != 1 {
			t.Fatalf("order %v: CountAt(5) = %d, want 1 (start wins)", order, got)
		}
		if got := countAt(t, c, 4); got != 1 {
			t.Fatalf("order %v: CountAt(4) = %d, want 1", order, got)
		}
		if got := countAt(t, c, 10); got != 0 {
			t.Fatalf("order %v: CountAt(10) = %d, want 0", order, got)
		}
	}
}

// 空区间与反向区间返回可判定错误。
func TestEmptyIntervalError(t *testing.T) {
	c := New()
	for _, iv := range []Interval{{5, 5}, {10, 3}, {0, 0}, {math.MaxInt64, math.MinInt64}} {
		if err := c.Add(iv.Lo, iv.Hi); !errors.Is(err, ErrEmptyInterval) {
			t.Fatalf("Add(%d, %d) err = %v, want ErrEmptyInterval", iv.Lo, iv.Hi, err)
		}
		if err := c.Remove(iv.Lo, iv.Hi); !errors.Is(err, ErrEmptyInterval) {
			t.Fatalf("Remove(%d, %d) err = %v, want ErrEmptyInterval", iv.Lo, iv.Hi, err)
		}
	}
}

// 极值端点：[MinInt64, MaxInt64) 正常工作，不溢出。
func TestExtremeEndpoints(t *testing.T) {
	c := New()
	mustAdd(t, c, math.MinInt64, math.MaxInt64)
	for _, p := range []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1} {
		if got := countAt(t, c, p); got != 1 {
			t.Fatalf("CountAt(%d) = %d, want 1", p, got)
		}
	}
	if got := countAt(t, c, math.MaxInt64); got != 0 {
		t.Fatalf("CountAt(MaxInt64) = %d, want 0", got)
	}
	// 与触及边界的普通区间重叠。
	mustAdd(t, c, math.MinInt64, 0)
	mustAdd(t, c, 0, math.MaxInt64)
	if got := countAt(t, c, math.MinInt64); got != 2 {
		t.Fatalf("CountAt(MinInt64) = %d, want 2", got)
	}
	if got := countAt(t, c, 0); got != 2 {
		t.Fatalf("CountAt(0) = %d, want 2", got)
	}
	if got := countAt(t, c, -1); got != 2 {
		t.Fatalf("CountAt(-1) = %d, want 2", got)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	mustRemove(t, c, math.MinInt64, math.MaxInt64)
	if got := countAt(t, c, 0); got != 1 {
		t.Fatalf("after remove: CountAt(0) = %d, want 1", got)
	}
}

// 同一区间不同实例互不影响：Remove 只作用于精确匹配的区间。
func TestRemoveMatchesExactInterval(t *testing.T) {
	c := New()
	mustAdd(t, c, 0, 10)
	if err := c.Remove(0, 5); !errors.Is(err, ErrNotPresent) {
		t.Fatalf("Remove(0,5) err = %v, want ErrNotPresent", err)
	}
	if err := c.Remove(5, 10); !errors.Is(err, ErrNotPresent) {
		t.Fatalf("Remove(5,10) err = %v, want ErrNotPresent", err)
	}
	if got := countAt(t, c, 7); got != 1 {
		t.Fatalf("CountAt(7) = %d, want 1", got)
	}
}
