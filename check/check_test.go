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

func must(t *testing.T, ok bool, msg string, args ...any) {
	if !ok {
		t.Fatalf(msg, args...)
	}
}

func TestCapacityAndErrors(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8} {
		q, _ := ring.New[int](n)
		v, ok := q.Dequeue()
		must(t, !ok && v == 0 && q.Len() == 0, "cap=%d: empty dequeue", n)
		for i := 0; i < n; i++ {
			must(t, q.Enqueue(i) == nil, "cap=%d: enqueue #%d", n, i+1)
		}
		must(t, errors.Is(q.Enqueue(-1), ring.ErrFull), "cap=%d: overflow", n)
		must(t, q.Len() == n && q.Full() && !q.Empty(), "cap=%d: full state", n)
		q.Dequeue()
		must(t, q.Enqueue(-1) == nil && q.Len() == n, "cap=%d: refill", n)
	}
	_, badCap := ring.New[int](0)
	p, c, _ := seq.Pair[int](1)
	_, emptyErr := c.Recv()
	must(t, errors.Is(badCap, ring.ErrBadCap), "New(0): %v", badCap)
	must(t, errors.Is(emptyErr, seq.ErrEmpty), "empty recv: %v", emptyErr)
	must(t, p.Send(1) == nil && errors.Is(p.Send(2), seq.ErrFull), "seq full")
	must(t, !errors.Is(badCap, ring.ErrFull) && !errors.Is(emptyErr, ring.ErrFull), "sentinels collide")
}

func TestRandomModelVsRef(t *testing.T) {
	q, _ := ring.New[int](8)
	var ref check.Ref[int]
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		must(t, !(q.Empty() && q.Full()) && q.Len() <= q.Cap(), "op %d: bad state", i)
		if rng.Intn(2) == 0 && ref.Len() < q.Cap() {
			must(t, q.Enqueue(i) == nil, "op %d: unexpected ErrFull", i)
			ref.Enqueue(i)
		} else {
			got, ok := q.Dequeue()
			want, wok := ref.Dequeue()
			must(t, ok == wok && got == want, "op %d: got %d,%v want %d,%v", i, got, ok, want, wok)
		}
	}
	must(t, q.Peak() <= q.Cap(), "peak %d > cap %d", q.Peak(), q.Cap())
}

func TestSacrificeSlotIsWrong(t *testing.T) {
	buf, r, w := make([]int, 4), 0, 0
	enq := func(v int) error {
		if (w+1)%len(buf) == r {
			return ring.ErrFull
		}
		buf[w], w = v, (w+1)%len(buf)
		return nil
	}
	must(t, enq(1) == nil && enq(2) == nil && enq(3) == nil, "first 3 enqueues")
	must(t, errors.Is(enq(4), ring.ErrFull), "sacrifice-slot full at 4th")
}
func TestConcurrentFIFO(t *testing.T) {
	for _, prods := range []int{1, 4} { // SPSC and MPSC
		q, _ := ring.New[int](64)
		var mu sync.Mutex
		for p := 0; p < prods; p++ {
			go func(p int) {
				for i := 1; i <= 2000; {
					mu.Lock()
					if q.Enqueue(p*2000+i) == nil {
						i++
					}
					mu.Unlock()
				}
			}(p)
		}
		last := map[int]int{}
		for got := 0; got < prods*2000; {
			mu.Lock()
			v, ok := q.Dequeue()
			mu.Unlock()
			if ok {
				id, i := (v-1)/2000, (v-1)%2000+1
				must(t, i == last[id]+1, "producer %d: #%d after #%d", id, i, last[id])
				last[id] = i
				got++
			}
		}
	}
}
