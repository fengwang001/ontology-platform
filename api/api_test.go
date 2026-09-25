package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// SelfCheck 内置序列必须通过，且不得触碰接收者自身状态。
func TestSelfCheck(t *testing.T) {
	for _, max := range []int{1, 2, 8} {
		p, err := api.New(max)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := p.Acquire()
		_ = p.Release(b)
		i0, t0 := p.Idle(), p.Total()
		if err := p.SelfCheck(); err != nil {
			t.Fatalf("maxIdle=%d SelfCheck: %v", max, err)
		}
		if p.Idle() != i0 || p.Total() != t0 {
			t.Fatal("SelfCheck must not mutate receiver state")
		}
	}
}

// 三类错误经公开门面同样可判定、互不相同。
func TestAPIErrors(t *testing.T) {
	_, eNew := api.New(0)
	p, _ := api.New(1)
	b, _ := p.Acquire()
	_ = p.Release(b)
	eDup := p.Release(b)
	eUnk := p.Release(api.Block{})
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"invalid maxIdle", eNew, api.ErrInvalidMaxIdle},
		{"duplicate release", eDup, api.ErrDuplicateRelease},
		{"unknown release", eUnk, api.ErrUnknownBlock},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s: err=%v", c.name, c.err)
		}
	}
	if eNew == eDup || eDup == eUnk || eNew == eUnk {
		t.Fatal("the three errors must be pairwise distinct")
	}
	if p.Idle() != 1 || p.Total() != 1 {
		t.Fatal("rejections changed accounting")
	}
}

// 并发（无 sleep，屏障两阶段）：无同一块被两个 goroutine 同时持有，
// 结束 Idle=Total=N 稳定守恒。
func TestAPIConcurrency(t *testing.T) {
	const N = 128
	p, _ := api.New(N)
	var all, done sync.WaitGroup
	all.Add(N)
	owners := map[api.Block]int{}
	var mu sync.Mutex
	for i := 0; i < N; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			b, err := p.Acquire()
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			owners[b]++
			mu.Unlock()
			all.Done()
			all.Wait() // 等所有人都持块，再统一释放
			if err := p.Release(b); err != nil {
				t.Error(err)
			}
		}()
	}
	done.Wait()
	for b, n := range owners {
		if n != 1 {
			t.Fatalf("block %s held by %d goroutines", b, n)
		}
	}
	if len(owners) != N {
		t.Fatalf("acquired %d distinct blocks, want %d", len(owners), N)
	}
	if p.Idle() != N || p.Total() != N {
		t.Fatalf("end idle=%d total=%d want %d", p.Idle(), p.Total(), N)
	}
}
