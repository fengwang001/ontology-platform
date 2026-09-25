package ipq

import (
	"fmt"
	"math/bits"
	"math/rand"
	"testing"
)

// checkHeap verifies the heap invariant and the id->index mapping.
func checkHeap(t *testing.T, h *Heap) {
	t.Helper()
	if len(h.pos) != len(h.items) {
		t.Fatalf("pos size %d != items size %d", len(h.pos), len(h.items))
	}
	for i, it := range h.items {
		if h.pos[it.ID] != i {
			t.Fatalf("pos[%q]=%d, want %d", it.ID, h.pos[it.ID], i)
		}
		if i > 0 && h.less(i, (i-1)/2) {
			t.Fatalf("heap property violated at index %d", i)
		}
	}
}

// TestHeapProperty hammers the heap with random push/update/delete/pop
// and re-verifies the invariant after every single operation.
func TestHeapProperty(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			h := New()
			live := map[string]bool{}
			var seq int64
			for i := 0; i < 600; i++ {
				id := fmt.Sprintf("id%d", rng.Intn(40))
				switch rng.Intn(5) {
				case 0, 1:
					if !live[id] {
						seq++
						h.Push(Item{ID: id, Pri: rng.Intn(30), Seq: seq})
						live[id] = true
					}
				case 2:
					if live[id] {
						h.Update(id, rng.Intn(30))
					}
				case 3:
					if live[id] {
						h.Delete(id)
						live[id] = false
					}
				case 4:
					if it, ok := h.Pop(); ok {
						delete(live, it.ID)
					}
				}
				checkHeap(t, h)
			}
		})
	}
}

// TestSiftComplexity proves update/delete walk a heap path: the number of
// nodes checked or swapped stays within a small constant times log m and
// never grows linearly with m. The counter is read directly (same
// package); no exported API exposes it.
func TestSiftComplexity(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		t.Run(fmt.Sprintf("m%d", m), func(t *testing.T) {
			h := New()
			for i := 0; i < m; i++ {
				h.Push(Item{ID: fmt.Sprintf("id%d", i), Pri: i, Seq: int64(i)})
			}
			limit := 8 * (bits.Len(uint(m)) + 1)
			cases := []struct {
				name string
				op   func()
			}{
				{"root-decrease-key", func() { h.Update(h.items[0].ID, -1) }},
				{"tail-increase-key", func() { h.Update(h.items[len(h.items)-1].ID, 3*m) }},
				{"root-increase-key", func() { h.Update(h.items[0].ID, 4*m) }},
				{"root-delete", func() { h.Delete(h.items[0].ID) }},
			}
			for _, c := range cases {
				c.op()
				if h.checked > limit {
					t.Fatalf("%s: checked %d nodes, limit %d (m=%d)", c.name, h.checked, limit, m)
				}
				if h.checked >= m/2 {
					t.Fatalf("%s: checked %d grows linearly with m=%d", c.name, h.checked, m)
				}
			}
		})
	}
}
