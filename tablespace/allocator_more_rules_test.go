package tablespace

import (
	"errors"
	"testing"
)

// 题面第二个例子：碎片分配不借用 SEG 区段内空闲页。
func TestSpecExampleTwoNoBorrow(t *testing.T) {
	a, _ := New(8, 4, 2)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	for want := 0; want < 4; want++ {
		mustAlloc(t, a, s1, -1, want, "s1 frag")
	}
	for want := 4; want < 8; want++ {
		mustAlloc(t, a, s2, -1, want, "s2 frag fills extent0")
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 8}, {1, StateFree, 0, 0},
	}, "extent0 FULLFRAG")
	mustAlloc(t, a, s1, -1, 8, "s1 seg extent1")
	s3 := a.NewSegment()
	mustFailAlloc(t, a, s3, -1, ErrNoSpace, "no FRAG/FREE despite free pages in SEG extent")
	mustFailAlloc(t, a, s3, 9, ErrNoSpace, "hint cannot help frag allocation")
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 8}, {1, StateSeg, 1, 1},
	}, "no state change after no-space")
	checkOwner(t, a, 9, 0, "page9 still free")

	// 反向：整区分配不借用 FRAG 区段内的空闲页。
	// 单区段 X=8,F=4：s1 碎片分到 4 页后进入整区模式，无 FREE 即无空间，
	// 即使所在 FRAG 区段还有空闲页 4..7。
	a2, _ := New(8, 4, 1)
	t1 := a2.NewSegment()
	for _, p := range []int{0, 1, 2, 3} {
		mustAlloc(t, a2, t1, -1, p, "t1 four frag pages")
	}
	checkExtents(t, a2, []ExtentState{{0, StateFrag, 0, 4}}, "FRAG with free pages")
	mustFailAlloc(t, a2, t1, -1, ErrNoSpace, "seg alloc must not borrow FRAG free pages")
	mustFailAlloc(t, a2, t1, 7, ErrNoSpace, "hint in FRAG cannot help seg alloc")
	checkExtents(t, a2, []ExtentState{{0, StateFrag, 0, 4}}, "state unchanged")
}

// FreePage 对他段页与空闲页的拒绝；拒绝不改变任何状态。
func TestFreePageRejections(t *testing.T) {
	a, _ := New(4, 4, 2)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	mustAlloc(t, a, s1, -1, 0, "s1 frag page0")
	mustAlloc(t, a, s2, -1, 1, "s2 frag page1")
	// SEG 区段内的空闲页即使区段归该段也不归该段所有，释放必须拒绝。
	a2, _ := New(4, 4, 2)
	t1 := a2.NewSegment()
	for _, p := range []int{0, 1, 2, 3} {
		mustAlloc(t, a2, t1, -1, p, "t1 frag")
	}
	mustAlloc(t, a2, t1, 5, 4, "new SEG extent page4, hint5 ignored")
	// 页 5、6、7 是 SEG(t1) 区段内的空闲页。
	if err := a2.FreePage(t1, 6); !errors.Is(err, ErrPageNotOwned) {
		t.Fatalf("free free-page inside own SEG extent: %v", err)
	}
	checkOwner(t, a2, 4, 1, "page4 still owned after rejection")
	checkUsed(t, a2, t1, 5, "used unchanged after rejection")
	if err := a.FreePage(s2, 0); !errors.Is(err, ErrPageNotOwned) {
		t.Fatalf("free other segment frag page: %v", err)
	}
	if err := a.FreePage(s1, 1); !errors.Is(err, ErrPageNotOwned) {
		t.Fatalf("free other segment frag page reverse: %v", err)
	}
	if err := a.FreePage(s1, 4); !errors.Is(err, ErrPageNotOwned) {
		t.Fatalf("free never-allocated page: %v", err)
	}
	if err := a.FreePage(s1, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("free page -1: %v", err)
	}
	if err := a.FreePage(s1, 8); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("free page 8 out of range: %v", err)
	}
	if err := a.FreePage(99, 0); !errors.Is(err, ErrNoSuchSegment) {
		t.Fatalf("free by missing segment: %v", err)
	}
	checkOwner(t, a, 0, 1, "ownership unchanged")
	checkOwner(t, a, 1, 2, "ownership unchanged")
	checkUsed(t, a, s1, 1, "used unchanged")
	checkUsed(t, a, s2, 1, "used unchanged")
}

// FreeSegment 后段号失效、号不复用；碎片区段保留他段页，独占区段归还。
func TestFreeSegmentInvalidatesAndNoReuse(t *testing.T) {
	a, _ := New(4, 4, 5)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	mustAlloc(t, a, s1, -1, 0, "s1 frag")
	mustAlloc(t, a, s1, -1, 1, "s1 frag")
	mustAlloc(t, a, s2, -1, 2, "s2 frag")
	// s1 used=2<4 仍走碎片，继续在区段 0 取页 3、4（hint 均被忽略）。
	mustAlloc(t, a, s1, 16, 3, "still frag: min free page 3, hint ignored")
	mustAlloc(t, a, s1, 8, 4, "still frag: min free page 4, hint ignored")
	// used=4>=F：无独占区段，取编号最小 FREE 区段 1 的页 8（不看 hint）。
	mustAlloc(t, a, s1, 9, 8, "new SEG extent ignores hint -> page8")
	checkExtents(t, a, []ExtentState{
		{0, StateFullFrag, 0, 4}, {1, StateFrag, 0, 1}, {2, StateSeg, 1, 1},
		{3, StateFree, 0, 0}, {4, StateFree, 0, 0},
	}, "before free segment")
	if err := a.FreeSegment(s1); err != nil {
		t.Fatal(err)
	}
	checkExtents(t, a, []ExtentState{
		{0, StateFrag, 0, 1}, {1, StateFree, 0, 0},
		{2, StateFree, 0, 0}, {3, StateFree, 0, 0}, {4, StateFree, 0, 0},
	}, "after FreeSegment(s1)")
	checkOwner(t, a, 2, 2, "s2 page survives")
	checkOwner(t, a, 0, 0, "s1 frag released")
	checkOwner(t, a, 8, 0, "s1 seg page released")
	if _, err := a.Used(s1); !errors.Is(err, ErrNoSuchSegment) {
		t.Fatalf("used on dead segment: %v", err)
	}
	if _, err := a.AllocPage(s1, -1); !errors.Is(err, ErrNoSuchSegment) {
		t.Fatalf("alloc by dead segment: %v", err)
	}
	if err := a.FreeSegment(s1); !errors.Is(err, ErrNoSuchSegment) {
		t.Fatalf("double FreeSegment: %v", err)
	}
	// 号不复用：新段是 3 而非 1。
	s3 := a.NewSegment()
	if s3 != 3 {
		t.Fatalf("new segment id=%d, want 3 (no reuse)", s3)
	}
	// s1 释放后，s2 再做碎片分配仍取区段 0 最小空闲页 0。
	mustAlloc(t, a, s2, -1, 0, "s2 reuses min free page of surviving FRAG")
}

// 构造参数与 hint 校验；参数错误优先级最高。
func TestInvalidArguments(t *testing.T) {
	for _, args := range [][3]int{
		{1, 1, 1}, {1025, 1, 1}, {8, 0, 1}, {8, 9, 1}, {8, 4, 0}, {8, 4, 100001},
	} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) err=%v, want ErrInvalidArgument", args, err)
		}
	}
	a, _ := New(8, 4, 2)
	s := a.NewSegment()
	mustFailAlloc(t, a, s, -2, ErrInvalidArgument, "hint < -1")
	mustFailAlloc(t, a, s, 16, ErrInvalidArgument, "hint out of range")
	mustFailAlloc(t, a, 99, 0, ErrNoSuchSegment, "missing segment, valid hint")
	if _, err := a.Used(-1); !errors.Is(err, ErrNoSuchSegment) {
		t.Fatalf("Used(-1): %v", err)
	}
	if _, err := a.PageOwner(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PageOwner(-1): %v", err)
	}
	if _, err := a.PageOwner(16); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PageOwner(16): %v", err)
	}
}

// 无空间不先转换区段：整区模式下无 FREE 时，FRAG 等其余状态保持不变。
func TestNoSpaceNoStateChange(t *testing.T) {
	// X=4,F=1：第一次分配后 used=1 即进入整区模式。
	a, _ := New(4, 1, 2)
	s1 := a.NewSegment()
	s2 := a.NewSegment()
	mustAlloc(t, a, s1, -1, 0, "")
	mustAlloc(t, a, s1, -1, 4, "s1 seg extent1")
	mustAlloc(t, a, s1, 5, 5, "")
	mustAlloc(t, a, s1, 6, 6, "")
	mustAlloc(t, a, s1, 7, 7, "") // 区段 1 用满
	mustAlloc(t, a, s2, -1, 1, "s2 frag ext0")
	// s2 一次碎片分配后 used=1>=F=1，进入整区模式；无 FREE 区段 -> 无空间。
	// s2 独占区段为零、FREE 为零 -> 无空间。
	mustFailAlloc(t, a, s2, -1, ErrNoSpace, "no space no change")
	before := a.ExtentStates()
	mustFailAlloc(t, a, s2, 3, ErrNoSpace, "still no space, hint in FRAG ignored")
	after := a.ExtentStates()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("state changed by rejected alloc: %+v -> %+v", before, after)
		}
	}
}
