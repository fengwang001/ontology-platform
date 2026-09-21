package coverage

import (
	"errors"
	"math"
	"testing"
)

func TestAddThreeTimesIsThreeLayers(t *testing.T) {
	c := New()
	for i := 0; i < 3; i++ {
		if err := c.Add(0, 10); err != nil {
			t.Fatalf("Add #%d: %v", i, err)
		}
	}
	for _, p := range []int64{0, 5, 9} {
		if got := c.CountAt(p).Count; got != 3 {
			t.Fatalf("CountAt(%d) = %d, want 3", p, got)
		}
	}
	if got := c.CountAt(10).Count; got != 0 {
		t.Fatalf("CountAt(hi) = %d, want 0", got)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRemovePeelsOneLayer(t *testing.T) {
	c := New()
	for i := 0; i < 3; i++ {
		_ = c.Add(0, 10)
	}
	if err := c.Remove(0, 10); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := c.CountAt(5).Count; got != 2 {
		t.Fatalf("after one Remove, CountAt(5) = %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		if err := c.Remove(0, 10); err != nil {
			t.Fatalf("Remove #%d: %v", i, err)
		}
	}
	if got := c.CountAt(5).Count; got != 0 {
		t.Fatalf("after all Removes, CountAt(5) = %d, want 0", got)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestOverRemoveAndUnknownAreDistinctErrors(t *testing.T) {
	c := New()
	if err := c.Add(1, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(1, 2); err != nil {
		t.Fatal(err)
	}
	// 已减到零后再减：判定为未跟踪。
	err := c.Remove(1, 2)
	if !errors.Is(err, ErrNotTracked) {
		t.Fatalf("want ErrNotTracked, got %v", err)
	}
	// 从未添加过的区间：同样可判定。
	if err := c.Remove(3, 4); !errors.Is(err, ErrNotTracked) {
		t.Fatalf("want ErrNotTracked, got %v", err)
	}
	// 层数已为 0 仍尝试再减：返回 ErrOverRemoved，且绝不产生负计数。
	var bare Counter
	bare.refs = map[interval]int64{{Lo: 9, Hi: 11}: 0}
	bare.delta = map[int64]int64{}
	if err := bare.Remove(9, 11); !errors.Is(err, ErrOverRemoved) {
		t.Fatalf("want ErrOverRemoved, got %v", err)
	}
	// 计数不得变负，状态不变。
	if got := c.CountAt(1).Count; got != 0 {
		t.Fatalf("CountAt(1) = %d, want 0", got)
	}
}

func TestEmptyIntervalError(t *testing.T) {
	c := New()
	if err := c.Add(5, 5); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("[5,5): want ErrEmptyInterval, got %v", err)
	}
	if err := c.Add(5, 4); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("[5,4): want ErrEmptyInterval, got %v", err)
	}
	if err := c.Remove(5, 5); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("remove [5,5): want ErrEmptyInterval, got %v", err)
	}
}

func TestExtremeEndpointsNoOverflow(t *testing.T) {
	c := New()
	if err := c.Add(math.MinInt64, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if got := c.CountAt(math.MinInt64).Count; got != 1 {
		t.Fatalf("CountAt(MinInt64) = %d, want 1", got)
	}
	if got := c.CountAt(0).Count; got != 1 {
		t.Fatalf("CountAt(0) = %d, want 1", got)
	}
	if got := c.CountAt(math.MaxInt64).Count; got != 0 {
		t.Fatalf("CountAt(MaxInt64) = %d, want 0", got)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
