package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// TestAPI exercises the external surface table-driven: alignment of every
// returned pointer, Allocated bookkeeping and the three sentinel kinds.
func TestAPI(t *testing.T) {
	a := api.New()
	cases := []struct{ size, al int }{{1, 1}, {10, 16}, {4, 8}, {7, 32}, {100, 64}}
	var ptrs []int
	for _, c := range cases {
		ptr, err := a.Alloc(c.size, c.al)
		if err != nil || ptr%c.al != 0 {
			t.Fatalf("Alloc(%d,%d): ptr=%d err=%v", c.size, c.al, ptr, err)
		}
		ptrs = append(ptrs, ptr)
	}
	if got := a.Allocated(); got != len(cases) {
		t.Fatalf("Allocated=%d, want %d", got, len(cases))
	}
	for i, p := range ptrs {
		if err := a.Free(p); err != nil {
			t.Fatalf("Free #%d: %v", i, err)
		}
		if err := a.Free(p); !errors.Is(err, api.ErrBadFree) {
			t.Fatalf("double Free #%d: %v", i, err)
		}
	}
	if got := a.Allocated(); got != 0 {
		t.Fatalf("Allocated=%d, want 0", got)
	}
	bad := []struct {
		op   func() error
		want error
	}{
		{func() error { _, e := a.Alloc(0, 8); return e }, api.ErrInvalidSize},
		{func() error { _, e := a.Alloc(4, 6); return e }, api.ErrInvalidAlign},
		{func() error { return a.Free(4242) }, api.ErrBadFree},
	}
	for i, b := range bad {
		if err := b.op(); !errors.Is(err, b.want) {
			t.Fatalf("bad op %d: got %v, want %v", i, err, b.want)
		}
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentAlloc: N goroutines each Alloc once; all pointers must be
// distinct and aligned, and concurrent Allocated reads must be monotonic.
func TestConcurrentAlloc(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		a := api.New()
		start := make(chan struct{})
		type res struct{ ptr, al int }
		out := make(chan res, n)
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func(al int) {
				defer wg.Done()
				<-start
				p, err := a.Alloc(8, al)
				if err != nil {
					t.Error(err)
					return
				}
				out <- res{p, al}
			}(1 << (g % 5))
		}
		done := make(chan []int)
		go func() { // reader: collect Allocated samples until all allocs land
			var samples []int
			for {
				v := a.Allocated()
				samples = append(samples, v)
				if v == n {
					break
				}
			}
			done <- samples
		}()
		close(start)
		wg.Wait()
		samples := <-done
		close(out)
		seen := map[int]bool{}
		count := 0
		for r := range out {
			if seen[r.ptr] || r.ptr%r.al != 0 {
				t.Fatalf("n=%d: dup or misaligned ptr %d (align %d)", n, r.ptr, r.al)
			}
			seen[r.ptr] = true
			count++
		}
		if count != n {
			t.Fatalf("n=%d: got %d pointers", n, count)
		}
		for i := 1; i < len(samples); i++ {
			if samples[i] < samples[i-1] {
				t.Fatalf("n=%d: Allocated not monotonic: %v...", n, samples[max(0, i-2):i+1])
			}
		}
	}
}
