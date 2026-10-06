package whiteboard

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func validateTreap(t *testing.T, tr *treap) []string {
	t.Helper()
	var check func(n *node, lo, hi string, hasBound bool)
	check = func(n *node, lo, hi string, hasBound bool) {
		if n == nil {
			return
		}
		if n.left != nil {
			if n.left.parent != n {
				t.Fatalf("left parent mismatch at %s", n.id)
			}
			if n.left.prio < n.prio {
				t.Fatalf("heap violated at %s/%s", n.id, n.left.id)
			}
		}
		if n.right != nil {
			if n.right.parent != n {
				t.Fatalf("right parent mismatch at %s", n.id)
			}
			if n.right.prio < n.prio {
				t.Fatalf("heap violated at %s/%s", n.id, n.right.id)
			}
		}
		if n.size != 1+n.left.subtreeSize()+n.right.subtreeSize() {
			t.Fatalf("size mismatch at %s", n.id)
		}
		check(n.left, lo, n.id, true)
		check(n.right, n.id, hi, true)
		_ = lo
		_ = hi
		_ = hasBound
	}
	check(tr.root, "", "", false)
	return tr.inorder()
}

func TestTreapInsertEraseRank(t *testing.T) {
	tr := &treap{}
	index := map[string]*node{}
	rng := rand.New(rand.NewPCG(42, 99))
	const n = 2000
	var want []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("e%04d", i)
		x := newTreapNode(id, rng.Uint64())
		pos := rng.IntN(i + 1)
		tr.insertAt(pos, x)
		index[id] = x
		want = append(want, "")
		copy(want[pos+1:], want[pos:])
		want[pos] = id
	}
	got := validateTreap(t, tr)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order mismatch after inserts")
	}
	for pos, id := range want {
		if r := tr.rankOf(index[id]); r != pos+1 {
			t.Fatalf("rank %s: got %d want %d", id, r, pos+1)
		}
		if tr.at(pos+1) != index[id] {
			t.Fatalf("at %d mismatch", pos+1)
		}
	}
	// 随机删除直到为空。
	for len(want) > 0 {
		pos := rng.IntN(len(want))
		id := want[pos]
		tr.erase(index[id])
		delete(index, id)
		want = append(want[:pos], want[pos+1:]...)
		got := validateTreap(t, tr)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("order mismatch after erase %s", id)
		}
		for p, wid := range want {
			if r := tr.rankOf(index[wid]); r != p+1 {
				t.Fatalf("post-erase rank %s: got %d want %d", wid, r, p+1)
			}
		}
	}
}

func TestTreapBetween(t *testing.T) {
	tr := &treap{}
	for i := 0; i < 10; i++ {
		tr.pushBack(newTreapNode(fmt.Sprintf("e%d", i), uint64(i+1)))
	}
	got := tr.between(3, 7)
	want := []string{"e2", "e3", "e4", "e5", "e6"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("between: %v", got)
	}
}

func TestTreapInorderSorted(t *testing.T) {
	tr := &treap{}
	rng := rand.New(rand.NewPCG(7, 7))
	var ids []string
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("k%03d", i)
		ids = append(ids, id)
		tr.pushBack(newTreapNode(id, rng.Uint64()))
	}
	got := tr.inorder()
	if fmt.Sprint(got) != fmt.Sprint(ids) {
		t.Fatalf("pushBack broke order")
	}
}
