package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dg"
)

// TestRejectNoTrace 钉住不变量 4：四类哨兵互不相同；任何拒绝都不留痕，
// 边数不变且实例仍可继续正常使用。表驱动多档 n。
func TestRejectNoTrace(t *testing.T) {
	sentinels := []error{api.ErrInvalidN, dg.ErrNodeOutOfRange, dg.ErrSelfLoop, dg.ErrDuplicateEdge}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] {
				t.Fatal("sentinels must be pairwise distinct")
			}
		}
	}
	for _, z := range []int{0, -1, -100} {
		if _, err := api.New(z); !errors.Is(err, api.ErrInvalidN) {
			t.Fatalf("New(%d) must fail with ErrInvalidN", z)
		}
	}
	for _, n := range []int{2, 3, 5, 50} {
		a, err := api.New(n)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.AddEdge(0, n-1); err != nil { // 预置一条合法边
			t.Fatal(err)
		}
		bads := []struct {
			u, v int
			want error
		}{
			{-1, 0, dg.ErrNodeOutOfRange},
			{0, n, dg.ErrNodeOutOfRange},
			{n, n, dg.ErrNodeOutOfRange},
			{0, 0, dg.ErrSelfLoop},
			{n - 1, n - 1, dg.ErrSelfLoop},
			{0, n - 1, dg.ErrDuplicateEdge},
		}
		before := a.EdgeCount()
		for _, b := range bads {
			err := a.AddEdge(b.u, b.v)
			if !errors.Is(err, b.want) {
				t.Fatalf("AddEdge(%d,%d)=%v want %v", b.u, b.v, err, b.want)
			}
		}
		if a.EdgeCount() != before { // 拒绝不留痕
			t.Fatalf("n=%d edge count changed %d->%d", n, before, a.EdgeCount())
		}
		if err := a.AddEdge(1, 0); err != nil || a.EdgeCount() != before+1 {
			t.Fatalf("n=%d graph not usable after rejection", n)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	for _, n := range []int{1, 5, 10} {
		a, err := api.New(n)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.SelfCheck(); err != nil {
			t.Fatalf("empty graph n=%d selfcheck: %v", n, err)
		}
	}
	a, _ := api.New(5)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}} {
		if err := a.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	a.Compute()
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("spec graph selfcheck: %v", err)
	}
}

// TestConcurrentAPI：Compute 后并发 Reach / EdgeCount / SelfCheck，
// 各 goroutine 的 Reach 结果逐对相同，race 干净；不使用 sleep。
func TestConcurrentAPI(t *testing.T) {
	a, _ := api.New(20)
	for i := 0; i < 19; i++ {
		_ = a.AddEdge(i, i+1)
	}
	_ = a.AddEdge(19, 0)
	a.Compute()
	const g, p = 16, 400
	res := make([][p]bool, g)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for x := 0; x < g; x++ {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()
			<-start
			for q := 0; q < p; q++ {
				res[x][q] = a.Reach(q/20, q%20)
			}
		}(x)
	}
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = a.SelfCheck()
			_ = a.EdgeCount()
		}()
	}
	close(start)
	wg.Wait()
	for x := 1; x < g; x++ {
		if res[x] != res[0] {
			t.Fatalf("goroutine %d disagrees", x)
		}
	}
}
