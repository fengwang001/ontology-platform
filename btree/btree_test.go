package btree

import "testing"

// TestSearchVisitedLogBound pins the visited-node counter of Search to a
// logarithmic bound: it must not grow linearly with the key count m.
func TestSearchVisitedLogBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		tr := New(m)
		for i := 1; i <= m; i++ {
			if _, err := tr.Insert(i); err != nil {
				t.Fatalf("m=%d insert %d: %v", m, i, err)
			}
		}
		for _, k := range []int{1, m / 2, m} {
			if !tr.Search(k) {
				t.Fatalf("m=%d: key %d not found", m, k)
			}
			if tr.visited > 40 {
				t.Fatalf("m=%d k=%d: visited %d nodes, want <= 40", m, k, tr.visited)
			}
		}
	}
}

// TestInsertSplitCosts pins the per-step split costs of ascending inserts
// 1..7, including the root cascade at step 7.
func TestInsertSplitCosts(t *testing.T) {
	tr := New(100)
	for k, want := range []int{0, 0, 1, 0, 1, 0, 2} {
		if c, err := tr.Insert(k + 1); err != nil || c != want {
			t.Fatalf("insert %d: cost=%d err=%v, want cost=%d", k+1, c, err, want)
		}
	}
}

// TestDeleteMergeCost pins Delete(3) on the 1..7 tree: two merges, no borrow.
func TestDeleteMergeCost(t *testing.T) {
	tr := New(100)
	for k := 1; k <= 7; k++ {
		tr.Insert(k)
	}
	if c, err := tr.Delete(3); err != nil || c != 2 {
		t.Fatalf("Delete(3): cost=%d err=%v, want cost=2", c, err)
	}
	if got := tr.Root.Inorder(nil); len(got) != 6 {
		t.Fatalf("after Delete(3): %v keys", got)
	}
}
