package api

import (
	"math/rand"
	"sync"
	"testing"

	"ontology/circ"
	"ontology/mec"
)

func insertAll(t *testing.T, a *API, pts []circ.Point) {
	t.Helper()
	for _, p := range pts {
		if err := a.Insert(p.X, p.Y); err != nil {
			t.Fatalf("Insert(%v) = %v", p, err)
		}
	}
}

func fixedSets() [][]circ.Point {
	return [][]circ.Point{
		{{X: 0, Y: 0}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 0, Y: 8}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 1, Y: 1}},
		{{X: 0, Y: 0}, {X: 3, Y: 0}, {X: 6, Y: 0}},
		{{X: 3, Y: 1}, {X: -4, Y: 2}, {X: 5, Y: -3}, {X: 0, Y: 7}, {X: -2, Y: -6}},
	}
}

// randomSets 用固定种子循环生成多档规模的随机点集。
func randomSets() [][]circ.Point {
	rng := rand.New(rand.NewSource(42))
	var out [][]circ.Point
	for _, n := range []int{1, 2, 3, 5, 8, 12} {
		for rep := 0; rep < 5; rep++ {
			seen := map[circ.Point]bool{}
			var s []circ.Point
			for len(s) < n {
				p := circ.Point{X: rng.Intn(101) - 50, Y: rng.Intn(101) - 50}
				if !seen[p] {
					seen[p] = true
					s = append(s, p)
				}
			}
			out = append(out, s)
		}
	}
	return out
}

// TestBruteForceConsistency 不变量 1、2：与暴力枚举逐字段相等（最小性）。
func TestBruteForceConsistency(t *testing.T) {
	for _, s := range append(fixedSets(), randomSets()...) {
		a, _ := New()
		insertAll(t, a, s)
		got, err := a.MinCircle()
		if err != nil {
			t.Fatal(err)
		}
		want := mec.BruteForce(s)
		if got.Cx.Cmp(want.Cx) != 0 || got.Cy.Cmp(want.Cy) != 0 || got.R2.Cmp(want.R2) != 0 {
			t.Fatalf("点集 %v：got (%v,%v) r²=%v，want (%v,%v) r²=%v",
				s, got.Cx, got.Cy, got.R2, want.Cx, want.Cy, want.R2)
		}
	}
}

// TestCoverageBoundary 不变量 3：全覆盖且边界点恰在圆上。
func TestCoverageBoundary(t *testing.T) {
	for _, s := range append(fixedSets(), randomSets()...) {
		a, _ := New()
		insertAll(t, a, s)
		got, _ := a.MinCircle()
		c := circ.Circle{Cx: got.Cx, Cy: got.Cy, R2: got.R2}
		for _, p := range s {
			if !c.Contains(p) {
				t.Fatalf("点集 %v：%v 未被覆盖", s, p)
			}
		}
		for _, b := range a.Boundary() {
			if !c.OnBoundary(b) {
				t.Fatalf("点集 %v：边界点 %v 不在圆上", s, b)
			}
		}
	}
}

// TestFailureNoTrace 不变量 4：三类可判定错误互不相同，被拒后状态不变。
func TestFailureNoTrace(t *testing.T) {
	a, _ := New()
	if _, err := a.MinCircle(); err != ErrEmpty {
		t.Fatalf("空集求圆 err=%v", err)
	}
	insertAll(t, a, []circ.Point{{X: 0, Y: 0}, {X: 6, Y: 0}})
	before, _ := a.MinCircle()
	if err := a.Insert(0, 0); err != ErrDuplicate {
		t.Fatalf("重复点 err=%v", err)
	}
	if err := a.Insert(10001, 0); err != ErrOutOfRange {
		t.Fatalf("越界 err=%v", err)
	}
	if ErrDuplicate == ErrOutOfRange || ErrDuplicate == ErrEmpty || ErrOutOfRange == ErrEmpty {
		t.Fatal("三类错误必须互不相同")
	}
	after, _ := a.MinCircle()
	if before.Cx.Cmp(after.Cx) != 0 || before.R2.Cmp(after.R2) != 0 || len(a.Boundary()) != 2 {
		t.Fatal("被拒操作改变了状态")
	}
	if err := a.Insert(3, 3); err != nil { // 拒绝后仍可正常使用
		t.Fatalf("拒绝后插入失败: %v", err)
	}
}

// TestConcurrentReads 并发只读结果逐字段相同，无 sleep。
func TestConcurrentReads(t *testing.T) {
	a, _ := New()
	insertAll(t, a, fixedSets()[5])
	want, _ := a.MinCircle()
	wantB := a.Boundary()
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got, err := a.MinCircle()
				if err != nil || got.Cx.Cmp(want.Cx) != 0 || got.Cy.Cmp(want.Cy) != 0 || got.R2.Cmp(want.R2) != 0 {
					errs <- "MinCircle 不一致"
					return
				}
				if len(a.Boundary()) != len(wantB) || a.SelfCheck() != nil {
					errs <- "Boundary/SelfCheck 不一致"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
