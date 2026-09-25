package api

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/mcsnode"
)

func TestGrantOrderMatchesNaive(t *testing.T) {
	scripts := map[string][]op{
		"six-step":    {{1, false}, {2, false}, {3, false}, {1, true}, {2, true}, {3, true}},
		"interleaved": {{1, false}, {2, false}, {1, true}, {3, false}, {2, true}, {4, false}, {3, true}, {4, true}},
	}
	for _, m := range []int{50, 200, 800} {
		for _, seed := range []uint32{7, 99} {
			scripts[fmt.Sprintf("gen-m%d-s%d", m, seed)] = genScript(m, seed)
		}
	}
	for name, s := range scripts {
		t.Run(name, func(t *testing.T) {
			if got, want := runScript(New(), s), naiveGrants(s); !slices.Equal(got, want) {
				t.Fatalf("grant order %v != naive %v", got, want)
			}
		})
	}
}

func TestFIFOFairness(t *testing.T) {
	for _, m := range []int{2, 8, 64, 256} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			ops := make([]op, 2*m)
			for i := 1; i <= m; i++ {
				ops[i-1], ops[m+i-1] = op{i, false}, op{i, true}
			}
			want := make([]int, m)
			for i := range want {
				want[i] = i + 1
			}
			if got := runScript(New(), ops); !slices.Equal(got, want) {
				t.Fatalf("grant order %v != enqueue order %v", got, want)
			}
		})
	}
}

func TestConcurrentMutualExclusion(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			l := New()
			var shared, cur, max atomic.Int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					nd, err := l.Acquire()
					if err != nil {
						t.Error(err)
						return
					}
					c := cur.Add(1)
					for m := max.Load(); c > m && !max.CompareAndSwap(m, c); m = max.Load() {
					}
					shared.Add(1)
					cur.Add(-1)
					if err := l.Release(nd); err != nil {
						t.Error(err)
					}
				}()
			}
			close(start)
			wg.Wait()
			if shared.Load() != int64(n) {
				t.Fatalf("shared counter = %d, want %d", shared.Load(), n)
			}
			if max.Load() != 1 {
				t.Fatalf("max concurrent holders = %d, want 1", max.Load())
			}
		})
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	l := New()
	n0, err := l.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	granted := make(chan *mcsnode.Node, 1)
	go func() { n, _ := l.Acquire(); granted <- n }()
	for len(l.Snapshot()) != 2 {
	}
	before := l.Snapshot()
	n1 := before[1]
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"nil token", func() error { return l.Release(nil) }, ErrNilNode},
		{"foreign node", func() error { return l.Release(mcsnode.New()) }, ErrNotOwner},
		{"queued non-holder", func() error { return l.Release(n1) }, ErrNotOwner},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); err != c.want {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
	if ErrNilNode == ErrNotOwner || ErrNilNode == ErrClosed || ErrNotOwner == ErrClosed {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	if after := l.Snapshot(); !slices.Equal(before, after) {
		t.Fatal("rejected ops mutated tail/node state")
	}
	if err := l.Release(n0); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(n0); err != ErrNotOwner {
		t.Fatalf("double release got %v, want ErrNotOwner", err)
	}
	if n := <-granted; n != n1 {
		t.Fatal("handoff granted wrong node")
	}
	if err := l.Release(n1); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // Close 是终态
		if _, err := l.Acquire(); err != ErrClosed {
			t.Fatalf("acquire after close got %v, want ErrClosed", err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
