package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

// TestSixEdgeGraph 不变量 1+2：第三节六边图的 PEO 与弦图判定。
func TestSixEdgeGraph(t *testing.T) {
	e, err := api.New(6)
	if err != nil {
		t.Fatal(err)
	}
	for _, uv := range [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}} {
		if err := e.AddEdge(uv[0], uv[1]); err != nil {
			t.Fatal(err)
		}
	}
	e.Compute()
	if got := e.PEO(); !slices.Equal(got, []int{5, 4, 3, 2, 1, 0}) {
		t.Fatalf("PEO = %v", got)
	}
	if !e.IsChordal() || e.FirstViolator() != -1 {
		t.Fatalf("chordal=%v violator=%d", e.IsChordal(), e.FirstViolator())
	}
	if e.EdgeCount() != 6 {
		t.Fatalf("EdgeCount = %d", e.EdgeCount())
	}
}

// TestC4 不变量 2：C4 非弦图，首个违规节点为 3。
func TestC4(t *testing.T) {
	e, err := api.New(4)
	if err != nil {
		t.Fatal(err)
	}
	for _, uv := range [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}} {
		if err := e.AddEdge(uv[0], uv[1]); err != nil {
			t.Fatal(err)
		}
	}
	e.Compute()
	if e.IsChordal() || e.FirstViolator() != 3 {
		t.Fatalf("chordal=%v violator=%d, want false/3", e.IsChordal(), e.FirstViolator())
	}
}

// TestRejectedOpsLeaveStateUnchanged 不变量 4：四类可判定错误互不相同、不留痕、可继续用。
func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrNonPositiveN) {
		t.Fatalf("New(0) err = %v", err)
	}
	if _, err := api.New(-3); !errors.Is(err, api.ErrNonPositiveN) {
		t.Fatalf("New(-3) err = %v", err)
	}
	e, err := api.New(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		u, v int
		want error
	}{
		{"u 越界", 4, 0, api.ErrNodeOutOfRange},
		{"v 越界", 0, -1, api.ErrNodeOutOfRange},
		{"自环", 2, 2, api.ErrSelfLoop},
		{"重复边同向", 0, 1, api.ErrDuplicateEdge},
		{"重复边反向", 1, 0, api.ErrDuplicateEdge},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		before := e.EdgeCount()
		if err := e.AddEdge(c.u, c.v); !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		} else {
			seen[c.want] = true
		}
		if e.EdgeCount() != before {
			t.Fatalf("%s: 被拒后边数变了", c.name)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("错误种类不互不相同: %v", seen)
	}
	if err := e.AddEdge(1, 2); err != nil || e.EdgeCount() != 2 {
		t.Fatalf("被拒后无法继续正常使用: %v", err)
	}
}

// TestConcurrentReads 并发：Compute 后 N 个 goroutine 读结果必须逐项相同。
func TestConcurrentReads(t *testing.T) {
	e, err := api.New(6)
	if err != nil {
		t.Fatal(err)
	}
	for _, uv := range [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}} {
		if err := e.AddEdge(uv[0], uv[1]); err != nil {
			t.Fatal(err)
		}
	}
	e.Compute()
	wantPEO := e.PEO()
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				if !e.IsChordal() || e.FirstViolator() != -1 {
					errs <- "verdict mismatch"
					return
				}
				if !slices.Equal(e.PEO(), wantPEO) {
					errs <- "peo mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

// TestSelfCheck 自检方法对内置图核验四条不变量。
func TestSelfCheck(t *testing.T) {
	e, err := api.New(1)
	if err != nil {
		t.Fatal(err)
	}
	e.Compute()
	if !e.IsChordal() || !slices.Equal(e.PEO(), []int{0}) {
		t.Fatalf("n=1 应为弦图且 PEO=[0]")
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
