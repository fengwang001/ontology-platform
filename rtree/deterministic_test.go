package rtree

import "testing"

func TestChooseAndSplitTies(t *testing.T) {
	tree, err := New(4, 2, 20)
	if err != nil {
		t.Fatal(err)
	}

	insertForTest(t, tree, 1, Rect{0, 0, 2, 2})
	insertForTest(t, tree, 2, Rect{4, 0, 6, 2})
	insertForTest(t, tree, 3, Rect{0, 4, 2, 6})
	insertForTest(t, tree, 4, Rect{4, 4, 6, 6})
	insertForTest(t, tree, 5, Rect{10, 0, 12, 2})

	want := "N[0 0 12 6]{L[0 0 6 6](1 3 2),L[4 0 12 6](4 5)}"
	if got := tree.Dump(); got != want {
		t.Fatalf("tie split dump = %q, want %q", got, want)
	}

	insertForTest(t, tree, 6, Rect{10, 4, 12, 6})
	insertForTest(t, tree, 7, Rect{1, 1, 5, 5})
	assertInvariants(t, tree, map[int64]Rect{
		1: {0, 0, 2, 2}, 2: {4, 0, 6, 2}, 3: {0, 4, 2, 6},
		4: {4, 4, 6, 6}, 5: {10, 0, 12, 2}, 6: {10, 4, 12, 6},
		7: {1, 1, 5, 5},
	})
}

func TestStableSplitKeyTie(t *testing.T) {
	tree, err := New(3, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	insertForTest(t, tree, 1, Rect{0, 0, 2, 2})
	insertForTest(t, tree, 2, Rect{1, 1, 3, 3})
	insertForTest(t, tree, 3, Rect{2, 0, 4, 2})
	insertForTest(t, tree, 4, Rect{10, 10, 12, 12})

	want := "N[0 0 12 12]{L[0 0 3 3](1 2),L[2 0 12 12](3 4)}"
	if got := tree.Dump(); got != want {
		t.Fatalf("stable split dump = %q, want %q", got, want)
	}
}

func TestRootGrowthAndNonRootPropagation(t *testing.T) {
	tree, err := New(3, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	rects := []Rect{
		{0, 0, 1, 1}, {3, 0, 4, 1}, {6, 0, 7, 1},
		{0, 3, 1, 4}, {3, 3, 4, 4}, {6, 3, 7, 4},
		{0, 6, 1, 7}, {3, 6, 4, 7}, {6, 6, 7, 7},
		{9, 0, 10, 1}, {9, 3, 10, 4}, {9, 6, 10, 7},
	}
	for index, rect := range rects {
		insertForTest(t, tree, int64(index+1), rect)
		assertInvariants(t, tree, rectsMap(rects[:index+1]))
	}
	if tree.root.height < 2 {
		t.Fatalf("root height = %d, expected propagated split", tree.root.height)
	}

	first := tree.Dump()
	second := tree.Dump()
	if first != second {
		t.Fatalf("dump not stable: %q != %q", first, second)
	}
}

func TestMinThresholdAndReinsertSplit(t *testing.T) {
	tree, err := New(4, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	rects := []Rect{
		{0, 0, 1, 1}, {3, 0, 4, 1}, {6, 0, 7, 1},
		{0, 3, 1, 4}, {6, 3, 7, 4}, {9, 0, 10, 1},
	}
	for index, rect := range rects {
		insertForTest(t, tree, int64(index+1), rect)
	}

	expected := rectsMap(rects)
	delete(expected, 1)
	if removed, reinserted, err := tree.Delete(1); err != nil || removed != 0 || reinserted != 0 {
		t.Fatalf("delete at min threshold = %d,%d,%v", removed, reinserted, err)
	}
	assertInvariants(t, tree, expected)

	delete(expected, 4)
	removed, reinserted, err := tree.Delete(4)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || reinserted != 1 {
		t.Fatalf("underflow delete = %d,%d, want 1,1", removed, reinserted)
	}
	assertInvariants(t, tree, expected)
}

func TestInternalSubtreeReinsertionAndRepeatedRootShrink(t *testing.T) {
	tree, err := New(4, 2, 100)
	if err != nil {
		t.Fatal(err)
	}

	side := int64(4)
	for id := int64(1); id <= 32; id++ {
		x := ((id - 1) % side) * 4
		y := ((id - 1) / side) * 4
		insertForTest(t, tree, id, Rect{x, y, x + 1, y + 1})
	}
	if tree.root.height < 2 {
		t.Fatalf("height = %d, expected multi-level setup", tree.root.height)
	}

	sawSubtree := false
	for id := int64(1); id <= 32; id++ {
		x := ((id - 1) % side) * 4
		y := ((id - 1) / side) * 4
		rect := Rect{x, y, x + 1, y + 1}
		_, _, subtreeReinserted, _, found := tree.deleteLocked(id, rect)
		if !found {
			t.Fatalf("delete %d not found", id)
		}
		delete(tree.objects, id)
		if subtreeReinserted > 0 {
			sawSubtree = true
		}
		expected := make(map[int64]Rect)
		for remaining := id + 1; remaining <= 32; remaining++ {
			x := ((remaining - 1) % side) * 4
			y := ((remaining - 1) / side) * 4
			expected[remaining] = Rect{x, y, x + 1, y + 1}
		}
		assertInvariants(t, tree, expected)
	}

	if !sawSubtree {
		t.Fatal("no internal-node subtree was reinserted")
	}
	if got := tree.Dump(); got != "L[]()" {
		t.Fatalf("final dump = %q, want empty leaf root", got)
	}
}

func insertForTest(t *testing.T, tree *RTree, id int64, rect Rect) {
	t.Helper()
	if _, err := tree.Insert(id, rect); err != nil {
		t.Fatalf("insert %d: %v", id, err)
	}
}

func rectsMap(rects []Rect) map[int64]Rect {
	result := make(map[int64]Rect, len(rects))
	for index, rect := range rects {
		result[int64(index+1)] = rect
	}
	return result
}
