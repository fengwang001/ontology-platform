package mmheap

import (
	"fmt"
	"math/bits"
	"math/rand"
	"testing"
)

// model is a naive multiset used as the reference implementation.
type model []int64

func (m *model) push(v int64) { *m = append(*m, v) }

func (m *model) take(min bool) (int64, bool) {
	s := *m
	if len(s) == 0 {
		return 0, false
	}
	k := 0
	for i, v := range s {
		if min && v < s[k] || !min && v > s[k] {
			k = i
		}
	}
	v := s[k]
	*m = append(s[:k:k], s[k+1:]...)
	return v, true
}

// TestStructure drives random operation sequences and, after every step,
// checks the level invariant (Check) and agreement with the naive model.
func TestStructure(t *testing.T) {
	t.Parallel()
	for _, seed := range []int64{1, 2, 3, 4} {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			var h Heap
			var m model
			for step := 0; step < 1500; step++ {
				min := r.Intn(2) == 0
				switch r.Intn(4) {
				case 0, 1:
					v := int64(r.Intn(101) - 50)
					h.Push(v)
					m.push(v)
				case 2, 3:
					var got, want int64
					var ok, wok bool
					if min {
						got, ok = h.DeleteMin()
						want, wok = m.take(true)
					} else {
						got, ok = h.DeleteMax()
						want, wok = m.take(false)
					}
					if got != want || ok != wok {
						t.Fatalf("step %d: delete min=%v = (%d,%v), want (%d,%v)",
							step, min, got, ok, want, wok)
					}
				}
				if err := h.Check(); err != nil {
					t.Fatalf("step %d: %v", step, err)
				}
				if h.Len() != len(m) {
					t.Fatalf("step %d: Len=%d, model=%d", step, h.Len(), len(m))
				}
			}
		})
	}
}

// TestComplexity proves updates walk a heap path: the inspected/swapped
// node count of one Push/DeleteMin/DeleteMax stays within a small
// constant times log2(m), independent of m's linear growth.
func TestComplexity(t *testing.T) {
	t.Parallel()
	lasts := map[int]int{}
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		t.Run(fmt.Sprint("m", m), func(t *testing.T) {
			var h Heap
			for i := 0; i < m; i++ {
				h.Push(int64(i * 37 % 101))
			}
			lim := 16 * bits.Len(uint(m+1)) // 16 * ceil(log2(m+1))
			for _, op := range []string{"Push", "DeleteMin", "DeleteMax"} {
				switch op {
				case "Push":
					h.Push(-1000) // worst case: new global min
				case "DeleteMin":
					h.DeleteMin()
				case "DeleteMax":
					h.DeleteMax()
				}
				if h.last > lim {
					t.Errorf("m=%d %s touched %d nodes, limit %d", m, op, h.last, lim)
				}
				if !h.ScaleOK(m) {
					t.Errorf("m=%d %s: ScaleOK reports super-logarithmic cost", m, op)
				}
				lasts[m] = max(lasts[m], h.last)
			}
		})
	}
	for _, pair := range [][2]int{{100, 1000}, {300, 3000}, {1000, 10000}} {
		small, big := lasts[pair[0]], lasts[pair[1]]
		if big > small*4 { // 10x elements may cost only ~1.3x log levels
			t.Errorf("cost grows too fast: m=%d -> %d, m=%d -> %d",
				pair[0], small, pair[1], big)
		}
	}
}
