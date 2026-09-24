package det

import "math/rand"
import "reflect"
import "sync"
import "sync/atomic"
import "testing"

type naive struct {
	w, h, mx int64
	arr      map[int64]bool
}

func (n *naive) feed(s int64) {
	n.arr[s] = s > n.h || n.arr[s] // covered arrivals (gaps/duplicates) stay ignored
	n.mx = max(n.mx, s)
	for {
		next := n.h + 1
		if n.arr[next] {
			n.h++
			continue
		}
		if n.mx-next >= n.w {
			n.h++
			continue
		}
		return
	}
}

func TestNaiveReference(t *testing.T) {
	for ci := 0; ci < 6; ci++ {
		w := 1 + int64(ci%4)
		d, n := New(w), &naive{w: w, arr: map[int64]bool{}}
		rng := rand.New(rand.NewSource(int64(ci)))
		for j := 0; j < 24; j++ {
			s := 1 + int64(rng.Intn(15))
			d.Feed(s)
			n.feed(s)
			if d.h != n.h {
				t.Fatalf("H=%d want %d", d.h, n.h)
			}
			gset := map[int64]bool{}
			for _, g := range d.gaps {
				gset[g] = true
			}
			for x := int64(1); x <= d.h; x++ { // arrived=>prefix, absent=>gap
				if n.arr[x] == gset[x] {
					t.Fatalf("x=%d arr=%v gap=%v", x, n.arr[x], gset[x])
				}
			}
			for x := range d.seen { // in-flight: arrived and above H
				if !n.arr[x] || x <= d.h {
					t.Fatalf("bogus seen %d", x)
				}
			}
		}
	}
}

func TestWindowNoEarlyGap(t *testing.T) {
	cases := []struct {
		w, h      int64
		seq, gaps []int64
	}{
		{2, 4, []int64{1, 2, 3, 4, 6}, nil},
		{1, 3, []int64{1, 3}, []int64{2}},
		{2, 7, []int64{1, 2, 3, 4, 6, 9}, []int64{5, 7}},
		{2, 11, []int64{1, 2, 3, 4, 6, 9, 2, 3, 6, 10, 11}, []int64{5, 7, 8}},
	}
	for _, tc := range cases {
		d := New(tc.w)
		for _, s := range tc.seq {
			d.Feed(s)
		}
		if h, g := d.h, d.gaps; h != tc.h || !reflect.DeepEqual(g, tc.gaps) {
			t.Fatalf("%v: H=%d gaps=%v want %d/%v", tc.seq, h, g, tc.h, tc.gaps)
		}
	}
}

func TestDuplicateIgnored(t *testing.T) {
	cases := [][][]int64{
		{{1, 2, 3, 4, 6, 9}, {1, 5, 7, 2, 9}},
		{{1, 3, 2}, {2, 1}},
	}
	for i, tc := range cases {
		d := New(int64(2 - i))
		for _, s := range tc[0] {
			d.Feed(s)
		}
		h0, g0, n0 := d.h, len(d.gaps), len(d.seen)
		for _, x := range tc[1] {
			d.Feed(x)
			if d.h != h0 || len(d.gaps) != g0 || len(d.seen) != n0 {
				t.Fatalf("dup %d changed state", x)
			}
		}
	}
}

func TestConstantWork(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		d := New(2)
		for s := int64(1); s <= m; s++ {
			d.Feed(s)
		}
		d.Feed(m + 1)
		if d.checks > 3 || d.h != m+1 {
			t.Fatalf("m=%d checks=%d h=%d", m, d.checks, d.h)
		}
	}
}

func TestConcurrentFeed(t *testing.T) {
	for _, n := range []int{10, 100, 1000} {
		d := New(int64(n))
		var stop atomic.Bool
		var wr, fw sync.WaitGroup
		for r := 0; r < 4; r++ {
			wr.Add(1)
			go func() {
				defer wr.Done()
				prev := int64(0)
				for !stop.Load() {
					if h := d.Watermark(); h < prev {
						t.Errorf("watermark decreased %d->%d", prev, h)
					} else {
						prev = h
					}
				}
			}()
		}
		for _, v := range rand.New(rand.NewSource(int64(n))).Perm(n) {
			fw.Add(1)
			go func(x int) {
				defer fw.Done()
				d.Feed(int64(x) + 1)
			}(v)
		}
		fw.Wait()
		stop.Store(true)
		wr.Wait()
		if d.Watermark() != int64(n) || len(d.Gaps()) != 0 {
			t.Fatalf("n=%d H=%d gaps=%v", n, d.Watermark(), d.Gaps())
		}
	}
}
