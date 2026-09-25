package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func TestNaiveMatch(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		x := New()
		type ent struct{ base, al int }
		next, live := 0, map[int]ent{}
		up := func(v, a int) int { return (v + a - 1) &^ (a - 1) }
		for op := 0; op < 300; op++ {
			if len(live) == 0 || rng.Intn(3) > 0 {
				n, al := 1+rng.Intn(40), 1<<uint(rng.Intn(6))
				p, err := x.Alloc(n, al)
				if err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
				b := next
				np := up(b+8, al)
				if p != np || p%al != 0 {
					t.Fatalf("seed %d: ptr %d naive %d (align %d)", seed, p, np, al)
				}
				live[p], next = ent{b, al}, b+n+(al-1)+8
			} else {
				var p int
				for q := range live {
					p = q
					break
				}
				if rng.Intn(2) == 0 {
					p += 100000 // never-allocated pointer
				}
				err := x.Free(p)
				_, known := live[p]
				if known != (err == nil) {
					t.Fatalf("seed %d: Free(%d) err=%v known=%v", seed, p, err, known)
				}
				if known {
					delete(live, p)
				}
			}
			if x.Allocated() != len(live) {
				t.Fatalf("seed %d: Allocated=%d naive=%d", seed, x.Allocated(), len(live))
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		run  func(x *Allocator) error
		want error
	}{
		{"bad size", func(x *Allocator) error { _, e := x.Alloc(0, 8); return e }, ErrBadSize},
		{"neg size", func(x *Allocator) error { _, e := x.Alloc(-1, 8); return e }, ErrBadSize},
		{"bad align", func(x *Allocator) error { _, e := x.Alloc(4, 6); return e }, ErrBadAlign},
		{"free unknown", func(x *Allocator) error { return x.Free(999) }, ErrInvalidFree},
	}
	for _, c := range cases {
		x := New()
		p, err := x.Alloc(10, 16)
		if err != nil {
			t.Fatal(err)
		}
		before := x.Allocated()
		if err := c.run(x); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if x.Allocated() != before {
			t.Fatalf("%s changed Allocated %d -> %d", c.name, before, x.Allocated())
		}
		if err := x.Free(p); err != nil {
			t.Fatalf("%s: allocator unusable: %v", c.name, err)
		}
	}
}

func TestConcurrentAlloc(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		x := New()
		start, stop := make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		// Reader: Allocated must be monotonic non-decreasing during the burst.
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := 0
			for {
				select {
				case <-stop:
					return
				default:
					cur := x.Allocated()
					if cur < prev {
						t.Errorf("n=%d: Allocated decreased %d -> %d", n, prev, cur)
						return
					}
					prev = cur
				}
			}
		}()
		ptrs := make([]int, n)
		als := make([]int, n)
		var gw sync.WaitGroup
		for i := range ptrs {
			als[i] = 1 << uint(i%7)
			gw.Add(1)
			go func(i int) {
				defer gw.Done()
				<-start
				p, err := x.Alloc(1+(i%17), als[i])
				if err != nil {
					t.Errorf("n=%d goroutine %d: %v", n, i, err)
					return
				}
				ptrs[i] = p
			}(i)
		}
		close(start)
		gw.Wait()
		close(stop)
		wg.Wait()
		seen := map[int]bool{}
		for i, p := range ptrs {
			if p == 0 || p%als[i] != 0 {
				t.Fatalf("n=%d: ptr %d not aligned to %d", n, p, als[i])
			}
			if seen[p] {
				t.Fatalf("n=%d: duplicate ptr %d", n, p)
			}
			seen[p] = true
		}
		if x.Allocated() != n {
			t.Fatalf("n=%d: Allocated=%d want %d", n, x.Allocated(), n)
		}
	}
}
