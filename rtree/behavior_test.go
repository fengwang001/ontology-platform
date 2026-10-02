package rtree

import (
	"errors"
	"sync"
	"testing"
)

func TestSearchTouchAndVisited(t *testing.T) {
	tree, err := New(4, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	rects := map[int64]Rect{
		1: {0, 0, 2, 2},
		2: {4, 0, 6, 2},
		3: {0, 4, 2, 6},
		4: {4, 4, 6, 6},
		5: {8, 8, 9, 9},
	}
	for id, rect := range rects {
		insertForTest(t, tree, id, rect)
	}

	for _, query := range []Rect{
		{2, 2, 4, 4},
		{0, 2, 6, 4},
		{2, 0, 4, 6},
	} {
		got, err := tree.Search(query)
		if err != nil {
			t.Fatal(err)
		}
		want := bruteForce(rects, query)
		if !sameIDs(got, want) {
			t.Fatalf("search %v = %v, want %v", query, got, want)
		}
	}

	if _, err := tree.Search(Rect{20, 20, 21, 21}); err != nil {
		t.Fatal(err)
	}
	if got := tree.visited.Load(); got != 1 {
		t.Fatalf("visited non-intersecting root = %d, want 1", got)
	}

	if _, err := tree.Search(Rect{0, 0, 10, 10}); err != nil {
		t.Fatal(err)
	}
	wantVisited := 1 + countVisitedChildren(tree.root, Rect{0, 0, 10, 10})
	if got := tree.visited.Load(); got != int64(wantVisited) {
		t.Fatalf("visited = %d, want %d", got, wantVisited)
	}
}

func TestErrorsAndRejectedOpsDoNotMutate(t *testing.T) {
	tree, err := New(3, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	insertForTest(t, tree, 1, Rect{0, 0, 1, 1})
	insertForTest(t, tree, 2, Rect{2, 2, 3, 3})
	before := tree.Dump()
	beforeVisited := int64(77)
	beforeLocated := int64(88)
	tree.visited.Store(beforeVisited)
	tree.located.Store(beforeLocated)

	if _, err := tree.Insert(-1, Rect{0, 0, 1, 2}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid id err = %v", err)
	}
	if _, err := tree.Insert(3, Rect{0, 0, -1, 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid rect err = %v", err)
	}
	if _, err := tree.Insert(1, Rect{0, 0, 1, 1}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, err := tree.Insert(3, Rect{0, 0, 1, 1}); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("full err = %v", err)
	}
	if _, _, err := tree.Delete(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid delete err = %v", err)
	}
	if _, _, err := tree.Delete(99); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing delete err = %v", err)
	}
	if _, err := tree.Search(Rect{0, 0, -1, 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid search err = %v", err)
	}
	if got := tree.Dump(); got != before {
		t.Fatalf("rejected operations mutated dump %q, want %q", got, before)
	}
	if got := tree.visited.Load(); got != beforeVisited {
		t.Fatalf("rejected operations changed visited to %d", got)
	}
	if got := tree.located.Load(); got != beforeLocated {
		t.Fatalf("rejected operations changed located to %d", got)
	}
}

func TestDeleteAllAndReinsert(t *testing.T) {
	tree, err := New(3, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[int64]Rect{}
	for id := int64(1); id <= 7; id++ {
		rect := Rect{id, id, id + 1, id + 1}
		insertForTest(t, tree, id, rect)
		expected[id] = rect
	}
	for id := int64(1); id <= 7; id++ {
		if _, _, err := tree.Delete(id); err != nil {
			t.Fatal(err)
		}
		delete(expected, id)
		assertInvariants(t, tree, expected)
	}
	if got := tree.Dump(); got != "L[]()" {
		t.Fatalf("empty dump = %q", got)
	}

	insertForTest(t, tree, 8, Rect{1, 1, 2, 2})
	if got := tree.Dump(); got != "L[1 1 2 2](8)" {
		t.Fatalf("reinsert dump = %q", got)
	}
}

func TestDeleteLocatedMultipleBranches(t *testing.T) {
	tree, err := New(4, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	rects := map[int64]Rect{
		1: {0, 0, 2, 2},
		2: {0, 0, 2, 2},
		3: {4, 4, 6, 6},
		4: {4, 4, 6, 6},
		5: {8, 0, 10, 2},
	}
	for id, rect := range rects {
		insertForTest(t, tree, id, rect)
	}

	if _, _, err := tree.Delete(1); err != nil {
		t.Fatal(err)
	}
	if got := tree.located.Load(); got < 2 {
		t.Fatalf("located = %d, expected duplicate-containing branches", got)
	}

	if got := tree.located.Load(); got < 0 {
		t.Fatalf("located = %d", got)
	}
}

func TestConcurrentSearchVisibility(t *testing.T) {
	tree, err := New(4, 2, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for id := int64(1); id <= 200; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			rect := Rect{id % 20, id % 17, id%20 + 1, id%17 + 1}
			if _, err := tree.Insert(id, rect); err != nil {
				t.Errorf("insert: %v", err)
			}
		}(id)
	}
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tree.Search(Rect{0, 0, 20, 20}); err != nil {
				t.Errorf("search: %v", err)
			}
		}()
	}
	wg.Wait()

	expected := map[int64]Rect{}
	for id := int64(1); id <= 200; id++ {
		expected[id] = Rect{id % 20, id % 17, id%20 + 1, id%17 + 1}
	}
	assertInvariants(t, tree, expected)
}

func countVisitedChildren(n *node, query Rect) int {
	visited := 0
	for _, item := range n.entries {
		if n.height == 0 || !rectsIntersect(item.child.rect, query) {
			continue
		}
		visited++
		visited += countVisitedChildren(item.child, query)
	}
	return visited
}
