package check_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/check"
	"ontology/ring"
	"ontology/seq"
)

func bad(t *testing.T, cond bool, format string, args ...any) {
	if !cond {
		t.Fatalf(format, args...)
	}
}
func TestCapacityAndErrors(t *testing.T) { // 语义 1、4
	_, err0 := ring.New[int](0)
	bad(t, errors.Is(err0, seq.ErrBadCap), "cap<=0: %v", err0)
	for _, cap := range []int{1, 4, 8} {
		b, _ := ring.New[int](cap)
		for i := 0; i < cap; i++ {
			bad(t, b.Enqueue(i) == nil, "cap=%d enqueue#%d", cap, i+1)
		}
		err := b.Enqueue(9)
		bad(t, errors.Is(err, seq.ErrFull), "cap=%d overflow: %v", cap, err)
		bad(t, b.Len() == cap && b.Full() && !b.Empty(), "cap=%d state after ErrFull", cap)
		_, ok := b.Dequeue()
		bad(t, ok && b.Enqueue(9) == nil, "cap=%d slot not reusable", cap)
	}
}
func TestFIFOVsRef(t *testing.T) { // 语义 2、3
	ops := []struct{ enq, deq int }{{3, 1}, {2, 3}, {3, 4}, {4, 4}, {1, 2}}
	b, _ := ring.New[int](4)
	var ref check.Ref[int]
	next := 0
	for _, op := range ops {
		for i := 0; i < op.enq; i++ {
			bad(t, b.Enqueue(next) == nil, "enqueue %d failed", next)
			ref.Enqueue(next)
			next++
		}
		for i := 0; i < op.deq; i++ {
			got, ok1 := b.Dequeue()
			want, ok2 := ref.Dequeue()
			bad(t, got == want && ok1 == ok2, "got (%v,%v), want (%v,%v)", got, ok1, want, ok2)
		}
		bad(t, !b.Empty() || !b.Full(), "empty and full at once")
	}
	v, ok := b.Dequeue()
	bad(t, v == 0 && !ok && b.Len() == 0, "empty dequeue = (%v,%v)", v, ok)
}
func TestWastedSlotRejectsFourth(t *testing.T) { // 第三节：方案 1 证伪
	r, w, n := 0, 0, 0
	for (w+1)%4 != r { // 牺牲一槽位：(w+1)%4 == r 判满
		w, n = (w+1)%4, n+1
	}
	bad(t, n == 3, "wasted-slot stored %d, want 3 (cap=4)", n)
}
func TestRandomOps(t *testing.T) { // 第四节 + 资源上界
	b, _ := ring.New[int](8)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		if rng.Intn(2) == 0 {
			b.Enqueue(i)
		} else {
			b.Dequeue()
		}
	}
	bad(t, b.MaxLen() <= 8 && b.Len() <= b.Cap(), "size bound exceeded: max=%d", b.MaxLen())
}
func TestConcurrent(t *testing.T) { // 第五节：SPSC 与 MPSC
	for _, p := range []int{1, 4} {
		const n = 5000
		b, _ := ring.New[int](8)
		var wg sync.WaitGroup
		for i := 0; i < p; i++ {
			wg.Add(1)
			go func(base int) {
				defer wg.Done()
				for v := base; v < base+n; v++ {
					for b.Enqueue(v) != nil {
					}
				}
			}(i * n)
		}
		cnt := make([]int, p)
		for got := 0; got < p*n; {
			v, ok := b.Dequeue()
			if !ok {
				continue
			}
			bad(t, v == v/n*n+cnt[v/n], "p=%d got %d, want %d", p, v, v/n*n+cnt[v/n])
			cnt[v/n], got = cnt[v/n]+1, got+1
		}
		wg.Wait()
	}
}
