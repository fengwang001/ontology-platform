package api_test

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/graph"
)

// TestSentinelErrors 四类故障各有可判定且互不相同的哨兵错误。
func TestSentinelErrors(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if _, err := api.New(n); !errors.Is(err, api.ErrInvalidMaxEdges) {
			t.Errorf("New(%d): %v", n, err)
		}
	}
	e, _ := api.New(2)
	rm := func(u, v string) error { _, _, err := e.RemoveEdge(u, v); return err }
	cases := []struct {
		name string
		got  error
		want error
	}{
		{"add empty u", e.AddEdge("", "b"), api.ErrEmptyNode},
		{"add empty v", e.AddEdge("a", ""), api.ErrEmptyNode},
		{"remove empty", rm("a", ""), api.ErrEmptyNode},
		{"remove missing", rm("a", "b"), api.ErrEdgeNotExist},
	}
	for _, c := range cases {
		if !errors.Is(c.got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	sentinels := []error{api.ErrInvalidMaxEdges, api.ErrEmptyNode, api.ErrEdgeNotExist, api.ErrTooManyEdges}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d not distinct", i, j)
			}
		}
	}
	_ = e.AddEdge("a", "b")
	_ = e.AddEdge("c", "d")
	if err := e.AddEdge("e", "f"); !errors.Is(err, api.ErrTooManyEdges) {
		t.Errorf("over limit: %v", err)
	}
	if err := e.AddEdge("a", "b"); err != nil { // 已存在的边加重数不算新增
		t.Errorf("multiplicity add on existing edge: %v", err)
	}
}

// TestRejectedOpsAtomic 不变量4：被拒操作不改变任何状态，之后引擎仍可用。
func TestRejectedOpsAtomic(t *testing.T) {
	e, _ := api.New(1)
	_ = e.AddEdge("a", "b")
	_ = e.AddEdge("a", "b") // 重数 2
	before := e.Pairs()
	rejects := []func() error{
		func() error { return e.AddEdge("", "x") },
		func() error { _, _, err := e.RemoveEdge("x", "y"); return err },
		func() error { return e.AddEdge("c", "d") },
	}
	for i, r := range rejects {
		if err := r(); err == nil {
			t.Fatalf("reject %d: want error", i)
		}
		if !slices.Equal(e.Pairs(), before) {
			t.Fatalf("reject %d changed R", i)
		}
	}
	if _, _, err := e.RemoveEdge("a", "b"); err != nil || !e.Reachable("a", "b") { // 重数也没被动：还要删两次
		t.Fatal("first remove after rejects: edge should still exist")
	}
	if _, _, err := e.RemoveEdge("a", "b"); err != nil || e.Reachable("a", "b") {
		t.Fatal("second remove after rejects: edge should be gone")
	}
}

// TestSelfCheck 自检方法对内置序列核验四条不变量。
func TestSelfCheck(t *testing.T) {
	if e, err := api.New(10); err != nil {
		t.Fatal(err)
	} else if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrent 并发读结果逐对相同；写者与读者并发后 R 等于朴素 BFS。不用 sleep。
func TestConcurrent(t *testing.T) {
	e, _ := api.New(100)
	ref := graph.New()
	for _, p := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}} {
		_ = e.AddEdge(p[0], p[1])
		ref.Add(p[0], p[1])
	}
	want := e.Pairs()
	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 100 && !bad.Load(); k++ {
				if !slices.Equal(e.Pairs(), want) {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent readers got different Pairs")
	}
	// 阶段2：一个写者做一串增删，读者并发读；结束后与朴素 BFS 对比。
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		for _, p := range [][2]string{{"x", "y"}, {"y", "z"}, {"z", "x"}, {"x", "y"}} {
			_ = e.AddEdge(p[0], p[1])
			ref.Add(p[0], p[1])
		}
		for _, p := range [][2]string{{"x", "y"}, {"y", "z"}, {"x", "y"}} {
			_, _, _ = e.RemoveEdge(p[0], p[1])
			ref.Remove(p[0], p[1])
		}
	}()
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for k := 0; k < 100; k++ {
				_ = e.Pairs()
				_ = e.Reachable("a", "d")
			}
		}()
	}
	wg2.Wait()
	if got := e.Pairs(); !slices.Equal(got, ref.NaivePairs()) {
		t.Fatalf("after concurrent writes: R %v != naive %v", got, ref.NaivePairs())
	}
}
