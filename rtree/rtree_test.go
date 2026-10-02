package rtree

import (
	"errors"
	"fmt"
	"testing"
)

func mustInsert(t *testing.T, tree *RTree, id int64, r Rect) InsertResult {
	t.Helper()
	res, err := tree.Insert(id, r)
	if err != nil {
		t.Fatalf("Insert(%d) unexpected error: %v", id, err)
	}
	return res
}

func TestWorkedExample(t *testing.T) {
	tree, err := New(4, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	rects := []Rect{
		{0, 0, 2, 2},
		{4, 0, 6, 2},
		{0, 4, 2, 6},
		{4, 4, 6, 6},
		{10, 0, 12, 2},
	}
	for i, r := range rects {
		res := mustInsert(t, tree, int64(i+1), r)
		t.Logf("input=Insert(%d, %v) output=splits:%d dump=%s", i+1, r, res.Splits, tree.Dump())
	}
	got := tree.Dump()
	want := "N[0 0 12 6]{L[0 0 6 6](1 3 2),L[4 0 12 6](4 5)}"
	if got != want {
		t.Fatalf("after 5 inserts:\ngot  %s\nwant %s", got, want)
	}
	if res := mustInsert(t, tree, 6, Rect{10, 4, 12, 6}); res.Splits != 0 {
		t.Fatalf("insert 6 produced %d splits", res.Splits)
	}
	mustInsert(t, tree, 7, Rect{1, 1, 5, 5})
	got = tree.Dump()
	want = "N[0 0 12 6]{L[0 0 6 6](1 3 2 7),L[4 0 12 6](4 5 6)}"
	if got != want {
		t.Fatalf("after 7 inserts:\ngot  %s\nwant %s", got, want)
	}

	ids, err := tree.Search(Rect{2, 2, 4, 4})
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []int64{1, 2, 3, 4, 7}
	if fmt.Sprint(ids) != fmt.Sprint(wantIDs) {
		t.Fatalf("search = %v, want %v (corner/edge touch counts)", ids, wantIDs)
	}

	if _, err := tree.Delete(1); err != nil {
		t.Fatal(err)
	}
	got = tree.Dump()
	want = "N[0 0 12 6]{L[0 0 6 6](3 2 7),L[4 0 12 6](4 5 6)}"
	if got != want {
		t.Fatalf("after delete 1:\ngot  %s\nwant %s", got, want)
	}

	if _, err := tree.Delete(3); err != nil {
		t.Fatal(err)
	}
	got = tree.Dump()
	want = "N[1 0 12 6]{L[1 0 6 5](2 7),L[4 0 12 6](4 5 6)}"
	if got != want {
		t.Fatalf("after delete 3:\ngot  %s\nwant %s", got, want)
	}

	res, err := tree.Delete(2)
	if err != nil {
		t.Fatal(err)
	}
	got = tree.Dump()
	want = "L[1 0 12 6](4 5 6 7)"
	if got != want {
		t.Fatalf("after delete 2:\ngot  %s\nwant %s", got, want)
	}
	if res.Removed != 1 || res.Reinserted != 1 {
		t.Fatalf("delete 2 result = %+v, want removed=1 reinserted=1", res)
	}
	t.Logf("judgement: 1 leaf detached, entry 7 reinserted; final dump=%s", got)
}

func TestChooseChildTieBreakers(t *testing.T) {
	// M=10, m=5. Eleven 2x2 squares: five per cluster at (0,0)/(100,100),
	// then a distant one overflows the root leaf into two 6x6 leaves of
	// equal area 36.
	tree, _ := New(10, 5, 100)
	id := int64(1)
	for _, off := range [][2]int64{{0, 0}, {2, 0}, {4, 0}, {0, 2}, {2, 2}, {0, 4}} {
		mustInsert(t, tree, id, Rect{off[0], off[1], off[0] + 2, off[1] + 2})
		id++
	}
	for _, off := range [][2]int64{{0, 0}, {4, 0}, {0, 4}, {4, 4}, {2, 2}} {
		mustInsert(t, tree, id, Rect{100 + off[0], 100 + off[1],
			100 + off[0] + 2, 100 + off[1] + 2})
		id++
	}
	if tree.root.height != 1 {
		t.Fatalf("expected height 1, dump=%s", tree.Dump())
	}
	if a, b := rectArea(tree.root.entries[0].rect), rectArea(tree.root.entries[1].rect); a != b {
		t.Fatalf("setup: unequal leaf areas %d vs %d", a, b)
	}
	// A point halfway between the clusters enlarges both 6x6 leaves equally.
	mustInsert(t, tree, 200, Rect{50, 50, 52, 52})
	if c := len(tree.root.entries[0].child.entries); c != 7 {
		t.Fatalf("index tie-break failed: left leaf has %d entries, dump=%s", c, tree.Dump())
	}
	t.Logf("judgement: equal enlargement+area -> smaller index, dump=%s", tree.Dump())
}

func TestSplitTieStableOrder(t *testing.T) {
	// M=3, m=1. Entries 1 and 2 share sort key (4,4); stable order wins.
	tree, _ := New(3, 1, 100)
	mustInsert(t, tree, 1, Rect{0, 0, 4, 4})
	mustInsert(t, tree, 2, Rect{1, 1, 3, 3})
	mustInsert(t, tree, 3, Rect{0, 0, 2, 2})
	mustInsert(t, tree, 4, Rect{2, 2, 4, 4})
	want := "N[0 0 4 4]{L[0 0 4 4](3 1),L[1 1 4 4](2 4)}"
	if got := tree.Dump(); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	t.Logf("judgement: k=ceil(4/2)=2; tied keys keep 1 before 2, dump=%s", tree.Dump())
}

func TestRootSplitGrowsHeight(t *testing.T) {
	tree, _ := New(3, 1, 100)
	for i, p := range [][2]int64{{0, 0}, {10, 10}, {20, 20}, {30, 30}} {
		res := mustInsert(t, tree, int64(i+1), Rect{p[0], p[1], p[0] + 1, p[1] + 1})
		if i == 3 && res.Splits != 1 {
			t.Fatalf("4th insert splits=%d, want 1", res.Splits)
		}
	}
	if tree.root.height != 1 || len(tree.root.entries) != 2 {
		t.Fatalf("root did not grow: %s", tree.Dump())
	}
	t.Logf("judgement: overflowing root leaf creates new root, dump=%s", tree.Dump())
}

func TestSplitPropagatesToParent(t *testing.T) {
	tree, _ := New(3, 1, 100)
	for i := int64(1); i <= 11; i++ {
		x := (i - 1) * 10
		mustInsert(t, tree, i, Rect{x, 0, x + 1, 1})
	}
	if tree.root.height != 2 {
		t.Fatalf("setup expected height 2, dump=%s", tree.Dump())
	}
	r := mustInsert(t, tree, 12, Rect{110, 0, 111, 1})
	if r.Splits < 2 {
		t.Fatalf("insert 12 splits=%d, want >= 2; dump=%s", r.Splits, tree.Dump())
	}
	if tree.root.height != 2 || len(tree.root.entries) != 3 {
		t.Fatalf("internal split should add a root child (root still internal): %s", tree.Dump())
	}
	t.Logf("judgement: full leaf + full internal parent split: splits=%d dump=%s", r.Splits, tree.Dump())
}

func TestMBRExpandAndTighten(t *testing.T) {
	tree, _ := New(4, 2, 100)
	mustInsert(t, tree, 1, Rect{0, 0, 1, 1})
	mustInsert(t, tree, 2, Rect{2, 2, 3, 3})
	mustInsert(t, tree, 3, Rect{10, 10, 11, 11})
	if got, want := tree.Dump(), "L[0 0 11 11](1 2 3)"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if _, err := tree.Delete(3); err != nil {
		t.Fatal(err)
	}
	if got, want := tree.Dump(), "L[0 0 3 3](1 2)"; got != want {
		t.Fatalf("MBR not tightened: got %s want %s", got, want)
	}
	t.Log("judgement: insert enlarges MBR, delete tightens MBR")
}

func findLeaf(n *node, id int64) *node {
	if n.height == 0 {
		for _, e := range n.entries {
			if e.id == id {
				return n
			}
		}
		return nil
	}
	for _, e := range n.entries {
		if leaf := findLeaf(e.child, id); leaf != nil {
			return leaf
		}
	}
	return nil
}

func TestDeleteAtMKeptAndOneBelowDetached(t *testing.T) {
	tree, _ := New(4, 2, 100)
	for i, p := range [][2]int64{{0, 0}, {10, 0}, {0, 10}, {10, 10}, {20, 20}} {
		mustInsert(t, tree, int64(i+1), Rect{p[0], p[1], p[0] + 1, p[1] + 1})
	}
	if tree.root.height != 1 {
		t.Fatalf("setup: dump=%s", tree.Dump())
	}
	idInFullLeaf := tree.root.entries[0].child.entries[0].id
	res, err := tree.Delete(idInFullLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || tree.root.height != 1 {
		t.Fatalf("exactly m entries should be kept: res=%+v dump=%s", res, tree.Dump())
	}
	t.Logf("judgement: count == m kept, dump=%s", tree.Dump())

	next := tree.root.entries[0].child.entries[0].id
	res, err = tree.Delete(next)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Fatalf("one below m should detach 1 node: res=%+v dump=%s", res, tree.Dump())
	}
	if err := checkInvariants(tree); err != nil {
		t.Fatalf("invariants: %v dump=%s", err, tree.Dump())
	}
	t.Logf("judgement: count == m-1 detached, res=%+v dump=%s", res, tree.Dump())
}

func TestInternalNodeReinsertByHeight(t *testing.T) {
	// Deterministically built height-2 tree:
	//   root: A(A1,A2) B(B1,B2); every leaf holds objects at (id,id).
	tree := buildHeight2Tree(t, 2, 5)
	res, err := tree.Delete(1)
	if err != nil {
		t.Fatal(err)
	}
	// A1 (objects 1,2) drops to 1 entry -> A1 detached; internal A then
	// has only child A2 -> A detached, and subtree A2 is reinserted at
	// its own height.
	if res.Removed != 2 || res.Reinserted != 2 {
		t.Fatalf("res=%+v want removed=2 reinserted=2; dump=%s", res, tree.Dump())
	}
	if tree.Count() != 7 {
		t.Fatalf("count=%d want 7; dump=%s", tree.Count(), tree.Dump())
	}
	if err := checkInvariants(tree); err != nil {
		t.Fatalf("invariants: %v dump=%s", err, tree.Dump())
	}
	t.Logf("judgement: detached=%d reinserted=%d incl. subtree; dump=%s",
		res.Removed, res.Reinserted, tree.Dump())
}

func TestReinsertTriggersSplit(t *testing.T) {
	// Internal B already has M=4 children; reinserted subtree A2 is a
	// 5th child and must make B split during the delete's reinsert phase.
	tree := buildHeight2Tree(t, 4, 4)
	res, err := tree.Delete(1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Reinserted != 2 {
		t.Fatalf("res=%+v dump=%s", res, tree.Dump())
	}
	if tree.root.height != 2 || len(tree.root.entries) != 2 ||
		len(tree.root.entries[1].child.entries) != 2 {
		t.Fatalf("B should split into 2 leaves: %s", tree.Dump())
	}
	if err := checkInvariants(tree); err != nil {
		t.Fatalf("invariants: %v dump=%s", err, tree.Dump())
	}
	t.Logf("judgement: reinserted subtree overflows internal node -> split; dump=%s",
		tree.Dump())
}

func TestRootShrinksMultipleLevels(t *testing.T) {
	// M=3,m=1: build height 3 then delete all but one object.
	tree, _ := New(3, 1, 200)
	for i := int64(1); i <= 20; i++ {
		x := (i - 1) * 10
		mustInsert(t, tree, i, Rect{x, 0, x + 1, 1})
	}
	if tree.root.height != 3 {
		t.Fatalf("setup height=%d dump=%s", tree.root.height, tree.Dump())
	}
	for i := int64(1); i <= 19; i++ {
		if _, err := tree.Delete(i); err != nil {
			t.Fatal(err)
		}
		if err := checkInvariants(tree); err != nil {
			t.Fatalf("after delete %d: %v dump=%s", i, err, tree.Dump())
		}
	}
	if tree.root.height != 0 || tree.Count() != 1 {
		t.Fatalf("root did not collapse to leaf: dump=%s", tree.Dump())
	}
	if got, want := tree.Dump(), "L[190 0 191 1](20)"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	t.Logf("judgement: root shrank 3 levels to leaf: %s", tree.Dump())
}

func TestDeleteAllThenInsert(t *testing.T) {
	tree, _ := New(3, 1, 100)
	for i := int64(1); i <= 7; i++ {
		mustInsert(t, tree, i, Rect{i, i, i + 1, i + 1})
	}
	for i := int64(1); i <= 7; i++ {
		if _, err := tree.Delete(i); err != nil {
			t.Fatal(err)
		}
	}
	if got := tree.Dump(); got != "L[]()" {
		t.Fatalf("empty tree dump=%q want L[]()", got)
	}
	if tree.root.height != 0 || tree.Count() != 0 {
		t.Fatalf("empty tree state: height=%d count=%d", tree.root.height, tree.Count())
	}
	mustInsert(t, tree, 42, Rect{-5, -5, 5, 5})
	if got, want := tree.Dump(), "L[-5 -5 5 5](42)"; got != want {
		t.Fatalf("reinsert after empty: got %s want %s", got, want)
	}
	t.Log("judgement: empty leaf root then insertion works")
}

func TestDegenerateRectsAndTouchIntersections(t *testing.T) {
	tree, _ := New(8, 2, 100)
	mustInsert(t, tree, 1, Rect{2, 2, 2, 2})
	mustInsert(t, tree, 2, Rect{0, 2, 2, 2})
	mustInsert(t, tree, 3, Rect{2, 0, 2, 2})
	mustInsert(t, tree, 4, Rect{2, 2, 4, 2})

	ids, err := tree.Search(Rect{2, 2, 2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids) != "[1 2 3 4]" {
		t.Fatalf("point touch ids=%v", ids)
	}
	ids, err = tree.Search(Rect{4, 2, 5, 3})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids) != "[4]" {
		t.Fatalf("edge touch ids=%v", ids)
	}
	ids, err = tree.Search(Rect{100, 100, 101, 101})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("disjoint search ids=%v", ids)
	}
	if v := tree.Visited(); v != 1 {
		t.Fatalf("visited=%d want 1 (only root)", v)
	}
	t.Log("judgement: degenerate rects; edge/corner touch intersect; disjoint visited=1")
}

func TestVisitedCountOnMultiLeafTree(t *testing.T) {
	tree, _ := New(3, 1, 100)
	for i := int64(1); i <= 6; i++ {
		x := (i - 1) * 10
		mustInsert(t, tree, i, Rect{x, 0, x + 1, 1})
	}
	if _, err := tree.Search(Rect{0, 0, 11, 1}); err != nil {
		t.Fatal(err)
	}
	// Root plus the single left leaf whose MBR intersects the window.
	if v := tree.Visited(); v != 2 {
		t.Fatalf("visited=%d want 2", v)
	}
	t.Logf("judgement: visited = 1 + intersected non-root nodes (%d), root disjoint always 1",
		tree.Visited())
}

func TestLocatedCountsContainingBranches(t *testing.T) {
	// Several overlapping leaf MBRs contain the target rectangle; locate
	// counts every node entered during deterministic entry-order DFS.
	tree, _ := New(6, 3, 100)
	mustInsert(t, tree, 1, Rect{0, 0, 10, 10})
	mustInsert(t, tree, 2, Rect{100, 100, 110, 110})
	mustInsert(t, tree, 3, Rect{-100, -100, 1000, 1000})
	mustInsert(t, tree, 4, Rect{-200, -200, -190, -190})
	mustInsert(t, tree, 5, Rect{200, 200, 210, 210})
	mustInsert(t, tree, 6, Rect{500, 500, 510, 510})
	mustInsert(t, tree, 7, Rect{-300, 500, -290, 510})
	mustInsert(t, tree, 8, Rect{600, -300, 610, -290})
	if tree.root.height != 1 {
		t.Fatalf("setup: dump=%s", tree.Dump())
	}
	target := tree.objects[2]
	containing := 0
	for _, e := range tree.root.entries {
		if contains(e.rect, target) {
			containing++
		}
	}
	if containing < 2 {
		t.Fatalf("setup: only %d leaves contain target, need >=2; dump=%s",
			containing, tree.Dump())
	}
	if _, err := tree.Delete(2); err != nil {
		t.Fatal(err)
	}
	if tree.Located() > containing+1 {
		t.Fatalf("located=%d exceeds 1+containing(%d)", tree.Located(), containing+1)
	}
	if err := checkInvariants(tree); err != nil {
		t.Fatalf("invariants: %v dump=%s", err, tree.Dump())
	}
	t.Logf("judgement: located=%d, containing non-root nodes=%d", tree.Located(), containing)
}

func TestErrorOrderingAndNoStateChange(t *testing.T) {
	tree, _ := New(4, 2, 2)
	mustInsert(t, tree, 1, Rect{0, 0, 1, 1})

	if _, err := tree.Insert(0, Rect{0, 0, 1, 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if _, err := tree.Insert(1, Rect{2, 0, 1, 0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if _, err := tree.Insert(1, Rect{0, 0, 1, 1}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("got %v want ErrDuplicate", err)
	}
	mustInsert(t, tree, 2, Rect{5, 5, 6, 6})
	if _, err := tree.Insert(3, Rect{0, 0, 1, 1}); !errors.Is(err, ErrFull) {
		t.Fatalf("got %v want ErrFull", err)
	}
	// Out-of-range coordinate is invalid before fullness is considered.
	if _, err := tree.Insert(3, Rect{0, 0, 1_000_000_001, 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if _, err := tree.Delete(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if _, err := tree.Delete(123); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if _, err := tree.Search(Rect{0, 0, -1, 0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if _, err := New(17, 8, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("M=17: got %v want ErrInvalid", err)
	}
	if _, err := New(4, 3, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("m>M/2: got %v want ErrInvalid", err)
	}
	if _, err := New(4, 2, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("C=0: got %v want ErrInvalid", err)
	}

	dumpBefore := tree.Dump()
	// Rejected operations above left the tree intact:
	if got := tree.Dump(); got != dumpBefore {
		t.Fatalf("state changed by rejected ops:\n%s\n%s", got, dumpBefore)
	}
	if tree.Count() != 2 {
		t.Fatalf("count=%d want 2", tree.Count())
	}
	t.Logf("judgement: error precedence invalid>duplicate/notfound>full; state intact: %s", dumpBefore)
}
