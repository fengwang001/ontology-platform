package boundary

import (
	"testing"

	"ontology/bitemporal"
)

func ptr(v int64) *int64 { return &v }

var (
	halfOpen = bitemporal.BoundaryConvention{StartClosed: bitemporal.Closed, EndClosed: bitemporal.Open}
	closed   = bitemporal.BoundaryConvention{StartClosed: bitemporal.Closed, EndClosed: bitemporal.Closed}
	fullOpen = bitemporal.BoundaryConvention{StartClosed: bitemporal.Open, EndClosed: bitemporal.Open}
)

func TestContains(t *testing.T) {
	iv := bitemporal.Interval{Start: ptr(3), End: ptr(7)}
	// [3,7)
	if !contains(iv, halfOpen, 3) || contains(iv, halfOpen, 7) || !contains(iv, halfOpen, 5) {
		t.Fatal("[3,7) 归属错误")
	}
	// [3,7]
	if !contains(iv, closed, 7) {
		t.Fatal("[3,7] 终点 7 应归属")
	}
	// (3,7)
	if contains(iv, fullOpen, 3) {
		t.Fatal("(3,7) 起点 3 不应归属")
	}
	// 无界 (-∞,7)
	unbounded := bitemporal.Interval{End: ptr(7)}
	if !contains(unbounded, halfOpen, -1_000_000) || contains(unbounded, halfOpen, 7) {
		t.Fatal("(-∞,7) 归属错误")
	}
}

func TestCompare(t *testing.T) {
	c := NewComparator()
	iv := bitemporal.Interval{Start: ptr(3), End: ptr(7)}

	t.Run("右端点开闭不同_归属改变", func(t *testing.T) {
		d := c.Compare(iv, halfOpen, closed)
		if !d.ChangesMembership || !d.HasWitness || d.WitnessPoint != 7 {
			t.Fatalf("应在时点 7 检出归属翻转，得到 %+v", d)
		}
	})
	t.Run("左端点开闭不同_归属改变", func(t *testing.T) {
		d := c.Compare(iv, halfOpen, fullOpen)
		if !d.ChangesMembership || d.WitnessPoint != 3 {
			t.Fatalf("应在时点 3 检出归属翻转，得到 %+v", d)
		}
	})
	t.Run("约定相同_无差异", func(t *testing.T) {
		d := c.Compare(iv, closed, closed)
		if d.ChangesMembership {
			t.Fatal("相同约定不应有差异")
		}
	})
	t.Run("无界端点闭合位差异_有限查询归属不变", func(t *testing.T) {
		// 两端无界：无论闭合位怎么写，任何有限查询点都属于区间。
		all := bitemporal.Interval{}
		d := c.Compare(all, closed, fullOpen)
		if d.ChangesMembership {
			t.Fatal("无界端点的闭合位差异不应改变有限查询时点的归属")
		}
	})
	t.Run("单侧无界_仅有限端点翻转可见", func(t *testing.T) {
		leftOpen := bitemporal.Interval{End: ptr(7)}
		if d := c.Compare(leftOpen, halfOpen, closed); !d.ChangesMembership ||
			d.WitnessPoint != 7 {
			t.Fatalf("应在终点 7 检出翻转，得到 %+v", d)
		}
	})
	t.Run("空集区间_翻转无归属点", func(t *testing.T) {
		empty := bitemporal.Interval{Start: ptr(5), End: ptr(5),
			StartClosed: bitemporal.Open, EndClosed: bitemporal.Open}
		// 对 (5,5) 而言，任何约定下 5 都不属于区间，闭合另一端也不产生归属差异。
		d := c.Compare(empty, fullOpen, halfOpen)
		if d.ChangesMembership {
			t.Fatal("空集区间不应产生归属差异")
		}
	})
}
