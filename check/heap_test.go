package check

import (
	"errors"
	"math"
	"math/rand"
	"ontology/heap"
	"reflect"
	"sync"
	"testing"
)

func drain(h *heap.Heap[int]) []int {
	z := []int{}
	for h.Len() > 0 {
		e, _ := h.Pop()
		z = append(z, e.Val)
	}
	return z
}

func wrongPop(v []int) ([]int, int) {
	r, last := v[0], len(v)-1
	v[0], v = v[last], v[:last]
	for i := 0; ; {
		l, s := 2*i+1, 2*i+1
		if l >= len(v) {
			break
		}
		if r := l + 1; r < len(v) && v[r] < v[l] {
			s = r
		}
		if v[i] <= v[s] {
			break
		}
		v[i], v[s] = v[s], v[i]
		i = s
	}
	return v, r
}

func seeded(h *heap.Heap[int]) map[int]int {
	m := map[int]int{}
	for _, v := range []int{3, 5, 7, 9, 11, 13} {
		m[v] = h.Push(v)
	}
	return m
}

func TestHeap(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"empty and errors", func(t *testing.T) {
			h, zero := heap.New[int](), heap.Entry[int]{}
			if e, ok := h.Peek(); ok || e != zero {
				t.Fatal("peek")
			}
			if e, ok := h.Pop(); ok || e != zero {
				t.Fatal("pop")
			}
			if err := h.DecreaseKey(0, 0); !errors.Is(err, heap.ErrUnknown) {
				t.Fatal(err)
			}
			id := h.Push(5)
			h.Pop()
			if err := h.DecreaseKey(id, 0); !errors.Is(err, heap.ErrGone) {
				t.Fatal(err)
			}
			id = h.Push(2)
			if err := h.DecreaseKey(id, 2); !errors.Is(err, heap.ErrNotSmaller) {
				t.Fatal(err)
			}
		}},
		{"sift-up order", func(t *testing.T) {
			h := heap.New[int]()
			ids := seeded(h)
			if err := h.DecreaseKey(ids[9], 1); err != nil {
				t.Fatal(err)
			}
			if got := drain(h); !reflect.DeepEqual(got, []int{1, 3, 5, 7, 11, 13}) {
				t.Fatal(got)
			}
		}},
		{"sift-down counterexample", func(t *testing.T) {
			v, got := []int{3, 5, 7, 9, 11, 13}, []int{}
			v[3] = 1
			for len(v) > 0 {
				var x int
				v, x = wrongPop(v)
				got = append(got, x)
			}
			if reflect.DeepEqual(got, []int{1, 3, 5, 7, 11, 13}) {
				t.Fatal(got)
			}
		}},
		{"brute reference", func(t *testing.T) {
			h, b, rnd := heap.New[int](), NewBruteHeap(), rand.New(rand.NewSource(1))
			for i := 0; i < 10000; i++ {
				v := rnd.Intn(100000)
				if id, bid := h.Push(v), b.Push(v); id != bid {
					t.Fatal("id")
				}
				if i > 0 && rnd.Intn(2) == 0 {
					id, nv := rnd.Intn(i+1), rnd.Intn(v+1)
					if (h.DecreaseKey(id, nv) == nil) != (b.DecreaseKey(id, nv) == nil) {
						t.Fatal("model")
					}
				}
			}
			ref := []int{}
			for _, e := range b.Drain() {
				ref = append(ref, e.Val)
			}
			if got := drain(h); !reflect.DeepEqual(got, ref) {
				t.Fatal("order")
			}
		}},
		{"swap bound", func(t *testing.T) {
			h, rnd, ids := heap.New[int](), rand.New(rand.NewSource(2)), []int{}
			for i := 0; i < 10000; i++ {
				ids = append(ids, h.Push(rnd.Intn(1e6)))
			}
			for range ids {
				id := ids[rnd.Intn(len(ids))]
				if h.DecreaseKey(id, rnd.Intn(1e6)) == nil && float64(h.LastSwaps()) > 2*math.Log2(10000)+2 {
					t.Fatal("swaps")
				}
			}
		}},
		{"concurrent readers", func(t *testing.T) {
			h := heap.New[int]()
			for i := 1000; i > 0; i-- {
				h.Push(i)
			}
			want, _ := h.Peek()
			var wg sync.WaitGroup
			errs := make(chan error, 16)
			for range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 1000 {
						if got, ok := h.Peek(); !ok || got != want || h.Len() != 1000 {
							errs <- errors.New("read")
							return
						}
					}
					errs <- nil
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
