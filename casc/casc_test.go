package casc

import (
	"slices"
	"sync"
	"testing"
)

var edges = [][2]int{{1, 0}, {2, 1}, {3, 1}, {4, 2}, {5, 2}, {6, 3}, {7, 4}, {8, 0}, {9, 8}, {10, 99}}

func seedSet(t *testing.T) *Set {
	s := New()
	for _, e := range edges {
		if err := s.Insert(e[0], e[1]); err != nil {
			t.Fatalf("seed %v: %v", e, err)
		}
	}
	return s
}

func TestDeleteMatchesBatch(t *testing.T) {
	for _, root := range []int{1, 2, 3, 4, 8} {
		s := seedSet(t)
		got, err := s.Delete(root)
		want := map[int]bool{root: true} // batch recompute to fixpoint
		for grow := true; grow; {
			grow = false
			for _, e := range edges {
				if want[e[1]] && !want[e[0]] {
					want[e[0]], grow = true, true
				}
			}
		}
		if err != nil || len(got) != len(want) {
			t.Fatalf("root %d: got %v,%v want %v", root, got, err, want)
		}
		for _, id := range got {
			if !want[id] {
				t.Fatalf("root %d: %d outside batch closure", root, id)
			}
		}
	}
}

func TestDeleteOrder(t *testing.T) {
	want := map[int][]int{
		1: {7, 4, 5, 2, 6, 3, 1}, 2: {7, 4, 5, 2},
		3: {6, 3}, 4: {7, 4}, 8: {9, 8},
	}
	parent := map[int]int{}
	for _, e := range edges {
		parent[e[0]] = e[1]
	}
	for root, exp := range want {
		s := seedSet(t)
		got, err := s.Delete(root)
		if err != nil || !slices.Equal(got, exp) {
			t.Fatalf("Delete(%d)=%v,%v want %v", root, got, err, exp)
		}
		pos := map[int]int{}
		for i, id := range got {
			pos[id] = i
		}
		for _, id := range got {
			if j, ok := pos[parent[id]]; ok && pos[id] > j {
				t.Fatalf("%d placed after parent %d", id, j)
			}
		}
	}
}

func TestReferentialIntegrity(t *testing.T) {
	for _, root := range []int{1, 2, 3, 8} {
		s := seedSet(t)
		pre := map[int]bool{}
		for _, id := range s.Orphans() {
			pre[id] = true
		}
		gone, err := s.Delete(root)
		if err != nil {
			t.Fatal(err)
		}
		dead := map[int]bool{}
		for _, id := range gone {
			dead[id] = true
		}
		for _, e := range edges {
			if s.Exists(e[0]) && e[1] != 0 && !s.Exists(e[1]) && !pre[e[0]] {
				t.Fatalf("root %d: %d newly dangles to %d", root, e[0], e[1])
			}
			if dead[e[1]] && s.Exists(e[0]) {
				t.Fatalf("root %d: child %d survived deleted parent", root, e[0])
			}
		}
	}
	s := New()
	for _, e := range [][2]int{{1, 0}, {20, 77}, {21, 20}, {22, 21}} {
		if err := s.Insert(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := s.CleanupOrphans()
	if !slices.Equal(got, []int{22, 21, 20}) || len(s.Orphans()) != 0 || !s.Exists(1) {
		t.Fatalf("cleanup=%v orphans=%v", got, s.Orphans())
	}
}

func TestComplexityBounded(t *testing.T) {
	const subtree = 7 // {1..7} rooted at 1
	for _, m := range []int{100, 1000, 10000} {
		s := seedSet(t)
		for i := range m {
			if err := s.Add(1000+i, 0); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Delete(1)
		if err != nil || len(got) != subtree || s.lastTraversed > subtree+1 {
			t.Fatalf("m=%d: got=%v trav=%d err=%v", m, got, s.lastTraversed, err)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	s := seedSet(t)
	want := []int{10, -1, 7, 4, 5, 2, 6, 3, 1}
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			for range 50 {
				got := append(append(slices.Clone(s.Orphans()), -1), deleteSetReadOnly(s, 1)...)
				if !slices.Equal(got, want) {
					t.Errorf("read differs: %v", got)
				}
			}
		}()
	}
	wg.Wait()
}

func deleteSetReadOnly(s *Set, root int) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []int
	s.postorderLocked(root, &out, nil)
	return out
}
