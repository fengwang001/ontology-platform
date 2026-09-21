package ontology

import (
	"math/rand"
	"slices"
	"testing"
)

var overlapSet = []Interval{
	{0, 10},
	{5, 15},
	{10, 20},
	{3, 7},
	{7, 12},
}

// 打乱添加顺序后 Segments 必须逐元素一致。
func TestSegmentsOrderIndependent(t *testing.T) {
	base := New()
	for _, iv := range overlapSet {
		mustAdd(t, base, iv.Lo, iv.Hi)
	}
	want := base.Segments()
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(overlapSet))
		c := New()
		for _, i := range perm {
			mustAdd(t, c, overlapSet[i].Lo, overlapSet[i].Hi)
		}
		got := c.Segments()
		if !slices.Equal(got, want) {
			t.Fatalf("trial %d perm %v:\n got %v\nwant %v", trial, perm, got, want)
		}
	}
}

// 相邻且 count 相同的段必须合并，零段不得出现。
func TestSegmentsMergeAndNoZero(t *testing.T) {
	c := New()
	// [0,10) 与 [10,20) 首尾相接且各一层，必须合并为 [0,20)。
	mustAdd(t, c, 0, 10)
	mustAdd(t, c, 10, 20)
	want := []Segment{{Lo: 0, Hi: 20, Count: 1}}
	if got := c.Segments(); !slices.Equal(got, want) {
		t.Fatalf("Segments = %v, want %v", got, want)
	}
	// 中间挖一个洞：[5,15) 加一层再减一层，视图回到合并的一段。
	mustAdd(t, c, 5, 15)
	mustRemove(t, c, 5, 15)
	if got := c.Segments(); !slices.Equal(got, want) {
		t.Fatalf("after add+remove: Segments = %v, want %v", got, want)
	}
	// 制造真实的零区间：叠一层后移除中段，视图分裂为两段，零段不出现。
	mustAdd(t, c, 5, 15)
	mustRemove(t, c, 0, 10)
	mustRemove(t, c, 10, 20)
	want = []Segment{{Lo: 5, Hi: 15, Count: 1}}
	if got := c.Segments(); !slices.Equal(got, want) {
		t.Fatalf("after removal: Segments = %v, want %v", got, want)
	}
	for _, s := range c.Segments() {
		if s.Count == 0 {
			t.Fatalf("zero-count segment present: %+v", s)
		}
	}
}

// 三个重叠区间的规范分段。
func TestSegmentsThreeOverlapping(t *testing.T) {
	c := New()
	mustAdd(t, c, 0, 10)
	mustAdd(t, c, 5, 15)
	mustAdd(t, c, 10, 20)
	want := []Segment{
		{Lo: 0, Hi: 5, Count: 1},
		{Lo: 5, Hi: 15, Count: 2},
		{Lo: 15, Hi: 20, Count: 1},
	}
	if got := c.Segments(); !slices.Equal(got, want) {
		t.Fatalf("Segments = %v, want %v", got, want)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Segments 返回的切片与内部状态互不影响。
func TestSegmentsSliceIsolation(t *testing.T) {
	c := New()
	mustAdd(t, c, 0, 10)
	segs := c.Segments()
	if len(segs) != 1 {
		t.Fatalf("len(Segments) = %d, want 1", len(segs))
	}
	segs[0].Count = 999
	segs[0].Lo = -100
	again := c.Segments()
	want := []Segment{{Lo: 0, Hi: 10, Count: 1}}
	if !slices.Equal(again, want) {
		t.Fatalf("after mutating returned slice: Segments = %v, want %v", again, want)
	}
	// 后续变更不影响先前返回的切片。
	mustAdd(t, c, 0, 10)
	if again[0].Count != 1 {
		t.Fatalf("previously returned slice changed: %v", again)
	}
}

// 空计数器的视图为空。
func TestSegmentsEmpty(t *testing.T) {
	c := New()
	if segs := c.Segments(); len(segs) != 0 {
		t.Fatalf("Segments = %v, want empty", segs)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
