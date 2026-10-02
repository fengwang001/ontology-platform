package tablespace

import (
	"errors"
	"testing"
)

func mustAlloc(t *testing.T, a *Allocator, s, hint, want int, reason string) {
	t.Helper()
	got, err := a.AllocPage(s, hint)
	if err != nil || got != want {
		t.Fatalf("%s: AllocPage(%d,%d)=(%d,%v), want (%d,nil)", reason, s, hint, got, err, want)
	}
}

func mustFailAlloc(t *testing.T, a *Allocator, s, hint int, want error, reason string) {
	t.Helper()
	got, err := a.AllocPage(s, hint)
	if !errors.Is(err, want) || got != -1 {
		t.Fatalf("%s: AllocPage(%d,%d)=(%d,%v), want (-1,%v)", reason, s, hint, got, err, want)
	}
}

func checkExtents(t *testing.T, a *Allocator, want []ExtentState, reason string) {
	t.Helper()
	got := a.ExtentStates()
	if len(got) != len(want) {
		t.Fatalf("%s: extent count=%d, want %d", reason, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: extent %d = %+v, want %+v\nfull=%v", reason, i, got[i], want[i], got)
		}
	}
}

func checkUsed(t *testing.T, a *Allocator, s, want int, reason string) {
	t.Helper()
	got, err := a.Used(s)
	if err != nil || got != want {
		t.Fatalf("%s: Used(%d)=(%d,%v), want %d", reason, s, got, err, want)
	}
}

func checkOwner(t *testing.T, a *Allocator, page, want int, reason string) {
	t.Helper()
	got, err := a.PageOwner(page)
	if err != nil || got != want {
		t.Fatalf("%s: PageOwner(%d)=(%d,%v), want %d", reason, page, got, err, want)
	}
}

// 题面第一个例子完整复现。
func TestSpecExampleOne(t *testing.T) {
	a, err := New(8, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	s1 := a.NewSegment()
	if s1 != 1 {
		t.Fatalf("first segment id = %d, want 1", s1)
	}
	for want := 0; want < 4; want++ {
		mustAlloc(t, a, s1, -1, want, "frag allocations of seg1")
	}
	checkUsed(t, a, s1, 4, "seg1 used 4")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 4}, {1, StateFree, 0, 0},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
	}, "after seg1 four frag pages")
	mustAlloc(t, a, s1, -1, 8, "first seg allocation skips FRAG")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 4}, {1, StateSeg, 1, 1},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
	}, "after seg1 takes extent 1")
	mustAlloc(t, a, s1, 12, 12, "hint hits owned extent")
	checkUsed(t, a, s1, 6, "seg1 used 6")

	s2 := a.NewSegment()
	mustAlloc(t, a, s2, -1, 4, "seg2 frag picks min free page, not most-recent 3")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 5}, {1, StateSeg, 1, 2},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
	}, "after seg2 frag")
	for p := 0; p < 4; p++ {
		checkOwner(t, a, p, 1, "seg1 frag pages")
	}
	checkOwner(t, a, 4, 2, "seg2 frag page")
	checkOwner(t, a, 8, 1, "seg1 seg page")
	checkOwner(t, a, 12, 1, "seg1 hinted page")
	checkOwner(t, a, 5, 0, "free page")
}

// used 恰为 F-1 仍走碎片；第 F 次分配前 used=F-1 仍是碎片，再下一次转整区。
func TestThresholdBoundary(t *testing.T) {
	a, _ := New(4, 3, 6)
	s := a.NewSegment()
	mustAlloc(t, a, s, -1, 0, "used0 frag")
	mustAlloc(t, a, s, -1, 1, "used1 frag")
	mustAlloc(t, a, s, -1, 2, "used2 frag (F-1)") // 分配前 used=2 < 3
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 3}, {1, StateFree, 0, 0},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
		{4, StateFree, 0, 0}, {5, StateFree, 0, 0},
	}, "three pages still in frag extent")
	// 第 4 次分配前 used=3 >= F，转整区，取区段 1。
	mustAlloc(t, a, s, -1, 4, "used3 flips to seg")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 3}, {1, StateSeg, 1, 1},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
		{4, StateFree, 0, 0}, {5, StateFree, 0, 0},
	}, "fourth allocation gets fresh SEG extent")
}

// 碎片分配选编号最小的 FRAG 区段，而非最近用过的区段。
func TestFragPicksSmallestFragExtent(t *testing.T) {
	// X=4,F=2：s1 用页 0,1 使区段 0 成为 FRAG(used2)，再整区占区段 1（SEG）；
	// s2 在区段 0 续分两页使其 FULLFRAG；s3 碎片取区段 2 成 FRAG。
	// 释放 s1 的页 0 使区段 0 回 FRAG 后，s1 的碎片分配必须回编号最小的区段 0。
	a, _ := New(4, 2, 6)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	s3 := a.NewSegment()
	mustAlloc(t, a, s1, -1, 0, "s1 frag ext0 page0")
	mustAlloc(t, a, s1, -1, 1, "s1 frag ext0 page1")
	mustAlloc(t, a, s1, -1, 4, "s1 seg extent1 page4")
	mustAlloc(t, a, s2, -1, 2, "s2 frag ext0 page2")
	mustAlloc(t, a, s2, -1, 3, "s2 frag ext0 page3 -> FULLFRAG")
	mustAlloc(t, a, s3, -1, 8, "s3 frag new extent2 page8")
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 4}, {1, StateSeg, 1, 1},
		{2, StateFrag, 0, 1}, {3, StateFree, 0, 0},
		{4, StateFree, 0, 0}, {5, StateFree, 0, 0},
	}, "ext0 FULLFRAG, ext1 SEG, ext2 FRAG")
	if err := a.FreePage(s1, 0); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 3}, {1, StateSeg, 1, 1},
		{2, StateFrag, 0, 1}, {3, StateFree, 0, 0},
		{4, StateFree, 0, 0}, {5, StateFree, 0, 0},
	}, "ext0 back to FRAG, ext1 still SEG")
	// s1 此时 used：碎片页 1（1 页）+ 独占页 4（1 页）= 2。
	// 再释放独占页 4 使 used 回落到 1 < F。
	if err := a.FreePage(s1, 4); err != nil {
		t.Fatal(err)
	}
	// 碎片分配：编号最小 FRAG 是区段 0（最近用过的是区段 1），取最小空闲页 0。
	mustAlloc(t, a, s1, -1, 0, "smallest FRAG is extent0, not recently-used extent2")
}

// FREE->FRAG 取最小页；FRAG 用满转 FULLFRAG，释放末页依次回 FRAG/FREE。
func TestFragFullCycle(t *testing.T) {
	// X=3,F=3：s1 三页填满区段 0（FULLFRAG），s2 三页填满区段 1。
	a, _ := New(3, 3, 3)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	mustAlloc(t, a, s1, -1, 0, "")
	mustAlloc(t, a, s1, -1, 1, "")
	mustAlloc(t, a, s1, -1, 2, "frag extent fills")
	mustAlloc(t, a, s2, -1, 3, "")
	mustAlloc(t, a, s2, -1, 4, "")
	mustAlloc(t, a, s2, -1, 5, "extent1 FULLFRAG")
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 3}, {1, StateFullFrag, 0, 3}, {2, StateFree, 0, 0},
	}, "two FULLFRAG extents")
	// 释放页 2：FULLFRAG -> FRAG。
	if err := a.FreePage(s1, 2); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 2}, {1, StateFullFrag, 0, 3}, {2, StateFree, 0, 0},
	}, "FULLFRAG -> FRAG after free")
	// 重新分配仍取页 2（最小空闲）。
	mustAlloc(t, a, s1, -1, 2, "realloc min page")
	// 释放全部碎片页，最后一页释放后 FRAG -> FREE。
	if err := a.FreePage(s1, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.FreePage(s1, 1); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 1}, {1, StateFullFrag, 0, 3}, {2, StateFree, 0, 0},
	}, "one page left in frag")
	if err := a.FreePage(s1, 2); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFree, 0, 0}, {1, StateFullFrag, 0, 3}, {2, StateFree, 0, 0},
	}, "last frag page freed -> FREE")
	checkOwner(t, a, 2, 0, "freed page has no owner")
}

// 释放使 used 回落到 F 以下后，重回碎片分配。
func TestUsedDropsBackToFrag(t *testing.T) {
	a, _ := New(4, 2, 4)
	s := a.NewSegment()
	mustAlloc(t, a, s, -1, 0, "frag")
	mustAlloc(t, a, s, -1, 1, "frag")
	mustAlloc(t, a, s, -1, 4, "seg extent 1") // used=2 >= F
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 2}, {1, StateSeg, 1, 1},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
	}, "")
	// 释放两页碎片：used 回到 1 < F。
	if err := a.FreePage(s, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.FreePage(s, 1); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFree, 0, 0}, {1, StateSeg, 1, 1},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
	}, "frag extent fully released")
	// 下一次分配走碎片：编号最小 FRAG 不存在，取编号最小 FREE——
	// 区段 0 已归还且编号最小，重新转 FRAG，而不是使用 s 独占区段 1 的空位。
	mustAlloc(t, a, s, 7, 0, "back to frag: smallest FREE, hint ignored")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 1}, {1, StateSeg, 1, 1},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0},
	}, "frag again, SEG free page not borrowed")
}

// hint 落在他段独占区段 / 已占页 / 非独占区段时被忽略，回退到队首最小空闲页。
func TestHintIgnoredCases(t *testing.T) {
	a, _ := New(4, 4, 4)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	for _, p := range []int{0, 1, 2, 3} {
		mustAlloc(t, a, s1, -1, p, "s1 frag")
	}
	for _, p := range []int{4, 5, 6, 7} {
		mustAlloc(t, a, s2, -1, p, "s2 frag fills extent 1")
	}
	// s1 整区：取 FREE 区段 2 转 SEG(s1)，得页 8。
	mustAlloc(t, a, s1, -1, 8, "s1 gets extent 2")
	// s2 整区：取 FREE 区段 3 转 SEG(s2)，得页 12。
	mustAlloc(t, a, s2, -1, 12, "s2 gets extent 3")

	// hint 落在他段独占区段：忽略，队首区段 2 最小空闲页是 9。
	mustAlloc(t, a, s1, 13, 9, "hint in other segment's extent ignored")
	// hint 落在自己独占区段但是已占页：忽略，最小空闲页是 10。
	mustAlloc(t, a, s1, 8, 10, "hint occupied page ignored")
	// hint 落在非独占（FRAG/FULLFRAG）区段：忽略。
	mustAlloc(t, a, s1, 0, 11, "hint in FULLFRAG extent ignored")
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 4}, {1, StateFullFrag, 0, 4},
		{2, StateSeg, 1, 4}, {3, StateSeg, 2, 1},
	}, "extent2 filled through queue, extent3 untouched")
}

// 新取区段时不看 hint：即使 hint 合法且指向别处空闲页，也取新区段最小页。
func TestNewExtentIgnoresHint(t *testing.T) {
	a, _ := New(4, 4, 4)
	s := a.NewSegment()
	for _, p := range []int{0, 1, 2, 3} {
		mustAlloc(t, a, s, -1, p, "frag pages")
	}
	mustAlloc(t, a, s, 6, 4, "new extent ignores hint, takes min page")
	checkOwner(t, a, 4, 1, "page4 allocated")
	checkOwner(t, a, 6, 0, "hinted page6 still free")
}

// 满区段腾出位置后排到非满队列队尾。
func TestFullExtentGoesToQueueTail(t *testing.T) {
	// X=2,F=1：独占区段 1 用满出队，此时队列 [2,3]；
	// 释放区段 1 一页后它排到队尾 [2,3,1]。
	a, _ := New(2, 1, 5)
	s := a.NewSegment()
	mustAlloc(t, a, s, -1, 0, "frag page extent0")
	mustAlloc(t, a, s, -1, 2, "seg extent1 page2")
	mustAlloc(t, a, s, -1, 3, "extent1 full -> dequeue")
	mustAlloc(t, a, s, -1, 4, "seg extent2 page4")
	mustAlloc(t, a, s, -1, 5, "extent2 full -> dequeue")
	mustAlloc(t, a, s, -1, 6, "seg extent3 page6")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 1}, {1, StateSeg, 1, 2},
		{2, StateSeg, 1, 2}, {3, StateSeg, 1, 1},
		{4, StateFree, 0, 0},
	}, "extent1,2 full; queue [3]")
	if err := a.FreePage(s, 2); err != nil {
		t.Fatal(err)
	}
	mustAlloc(t, a, s, -1, 7, "head extent3 -> page7")
	mustAlloc(t, a, s, -1, 2, "requeued extent1 now head -> page2")
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 1}, {1, StateSeg, 1, 2},
		{2, StateSeg, 1, 2}, {3, StateSeg, 1, 2},
		{4, StateFree, 0, 0},
	}, "all three owned extents full again")
}

// 独占区段释放到全空归还 FREE，并从队列移除（含稀疏页情形）。
func TestOwnedExtentReturnedToFree(t *testing.T) {
	a, _ := New(3, 3, 3)
	s := a.NewSegment()
	for _, p := range []int{0, 1, 2} {
		mustAlloc(t, a, s, -1, p, "frag")
	}
	mustAlloc(t, a, s, 7, 3, "new extent ignores hint, page3")
	mustAlloc(t, a, s, 5, 5, "hint sparse page5 in extent1")
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 3}, {1, StateSeg, 1, 2}, {2, StateFree, 0, 0},
	}, "")
	if err := a.FreePage(s, 3); err != nil {
		t.Fatal(err)
	}
	if err := a.FreePage(s, 5); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 3}, {1, StateFree, 0, 0}, {2, StateFree, 0, 0},
	}, "sparse owned extent emptied -> FREE")
	mustAlloc(t, a, s, -1, 3, "extent1 re-acquired as smallest FREE")
}
