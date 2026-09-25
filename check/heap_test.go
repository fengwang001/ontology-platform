package check

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/heap"
	"ontology/item"
)

func newIntHeap() *heap.Heap[int] { return heap.New(func(a, b int) bool { return a < b }) }

func TestEmpty(t *testing.T) {
	cases := []struct {
		name string
		run  func() (int, bool)
	}{
		{"peek", func() (int, bool) { return newIntHeap().Peek() }},
		{"pop", func() (int, bool) { _, v, ok := newIntHeap().Pop(); return v, ok }},
	}
	for _, tc := range cases {
		if v, ok := tc.run(); ok || v != 0 {
			t.Errorf("%s: got (%d,%v), want (0,false)", tc.name, v, ok)
		}
	}
}

func TestMinOrderAndDecrease(t *testing.T) {
	// 反例堆序列：[3,5,7,9,11,13]，把键 9 降到 1。
	values := []int{3, 5, 7, 9, 11, 13}
	h := newIntHeap()
	ids := map[int]int{}
	for _, v := range values {
		ids[v] = h.Push(v)
	}
	if err := h.DecreaseKey(ids[9], 1); err != nil {
		t.Fatalf("decrease: %v", err)
	}
	want := []int{1, 3, 5, 7, 11, 13}
	for _, w := range want {
		_, got, ok := h.Pop()
		if !ok || got != w {
			t.Fatalf("pop: got (%d,%v), want %d", got, ok, w)
		}
	}
}

// badDownHeap is the inlined buggy implementation: after decrease it only
// sifts down instead of sifting up.
type badDownHeap struct{ body []int }

func (b *badDownHeap) push(v int) { b.body = append(b.body, v) }

func (b *badDownHeap) decreaseAtLeaf(v int) {
	i := len(b.body) - 1
	b.body[i] = v
	// Deliberately sift DOWN on a leaf: nothing moves, parent violation stays.
	n := len(b.body)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		c := l
		if r := l + 1; r < n && b.body[r] < b.body[l] {
			c = r
		}
		if b.body[c] >= b.body[i] {
			return
		}
		b.body[i], b.body[c] = b.body[c], b.body[i]
		i = c
	}
}

func (b *badDownHeap) popAll() []int {
	out := []int{}
	for len(b.body) > 0 {
		out = append(out, b.body[0])
		b.body = append([]int{}, b.body[1:]...)
	}
	return out
}

func TestBadSiftDownFails(t *testing.T) {
	// 叶节点 9 在 [1,4,3,9,5]，降到 0；错误实现无法把 0 送到根。
	b := &badDownHeap{}
	for _, v := range []int{1, 4, 3, 9, 5} {
		b.push(v)
	}
	b.decreaseAtLeaf(0)
	if b.body[0] == 0 {
		t.Fatal("buggy sift-down unexpectedly restored heap root")
	}
	// Peek stays wrong (1 instead of 0): min-heap invariant is broken.
	if got := b.popAll(); len(got) == 0 || got[0] != 1 {
		t.Fatalf("buggy heap peek = %v, want stale 1", got)
	}
}

func TestDecreaseErrors(t *testing.T) {
	h := heap.New(item.Less)
	id := h.Push(item.Item{Name: "q", Cost: 10})
	cases := []struct {
		name string
		id   int
		it   item.Item
		want error
	}{
		{"equal rejected", id, item.Item{Cost: 10}, item.ErrNotSmaller},
		{"larger rejected", id, item.Item{Cost: 20}, item.ErrNotSmaller},
		{"illegal negative", -1, item.Item{Cost: 1}, item.ErrUnknown},
		{"never issued", 999, item.Item{Cost: 1}, item.ErrUnknown},
	}
	for _, tc := range cases {
		if err := h.DecreaseKey(tc.id, tc.it); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, _, ok := h.Pop(); !ok {
		t.Fatal("pop")
	}
	if err := h.DecreaseKey(id, item.Item{Cost: 1}); !errors.Is(err, item.ErrGone) {
		t.Errorf("gone: got %v, want %v", err, item.ErrGone)
	}
}

func TestMatchesNaive(t *testing.T) {
	h, ref := heap.New(item.Less), NewNaive()
	rng := rand.New(rand.NewSource(1))
	ids := []int{}
	active := map[int]bool{}
	nextPush := 0
	decCount := map[int]int{}
	for step := 0; step < 2000; step++ {
		switch rng.Intn(3) {
		case 0:
			nextPush++
			it := item.Item{Cost: nextPush}
			id := h.Push(it)
			ids = append(ids, id)
			ref.Push(it)
			active[id] = true
		case 1:
			live := make([]int, 0, len(active))
			for id := range active {
				live = append(live, id)
			}
			if len(live) > 0 {
				id := live[rng.Intn(len(live))]
				decCount[id]++
				// Per-id strictly decreasing; the -id offset keeps keys distinct.
				it := item.Item{Cost: -(id + 1) - decCount[id]*100000}
				err := h.DecreaseKey(id, it)
				rejected := ref.DecreaseKey(id, it)
				if (err != nil) != rejected {
					t.Fatalf("step %d: heap err %v vs ref rejected %v", step, err, rejected)
				}
			}
		case 2:
			if h.Len() > 0 {
				hid, hv, _ := h.Pop()
				rid, rv, _ := ref.Pop()
				if hid != rid || hv != rv {
					t.Fatalf("step %d: heap (%d,%v) vs ref (%d,%v)", step, hid, hv, rid, rv)
				}
				delete(active, hid)
			}
		}
		if h.Len() != ref.Len() {
			t.Fatalf("step %d: len %d vs %d", step, h.Len(), ref.Len())
		}
	}
}

func TestSwapBound(t *testing.T) {
	h := newIntHeap()
	rng := rand.New(rand.NewSource(7))
	ids := make([]int, 10000)
	// Descending unique keys: each pushed value sifts to the root.
	for i := range ids {
		ids[i] = h.Push(1_000_000 - i)
	}
	bound := 2*math.Log2(float64(len(ids))) + 2
	for i := 0; i < 10000; i++ {
		id := ids[rng.Intn(len(ids))]
		v := rng.Intn(1_000_000)
		err := h.DecreaseKey(id, v)
		if err == nil && float64(h.LastDecreaseSwaps()) > bound {
			t.Fatalf("decrease %d swaps %d > bound %.2f", i, h.LastDecreaseSwaps(), bound)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	h := newIntHeap()
	for _, v := range []int{3, 1, 4, 1, 5} {
		h.Push(v)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				v, ok := h.Peek()
				if !ok || v != 1 || h.Len() != 5 {
					t.Errorf("inconsistent read: v=%d ok=%v len=%d", v, ok, h.Len())
					return
				}
			}
		}()
	}
	wg.Wait()
}
