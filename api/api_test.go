package api_test

import (
	"errors"
	"math/big"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func pt(x, y int) api.Point { return api.Point{X: x, Y: y} }

func ratOf(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }

func circleEq(c api.Circle, cx, cy, r2 string) bool {
	return c.Cx.Cmp(ratOf(cx)) == 0 && c.Cy.Cmp(ratOf(cy)) == 0 && c.R2.Cmp(ratOf(r2)) == 0
}

func sameCircle(a, b api.Circle) bool {
	return a.Cx.Cmp(b.Cx) == 0 && a.Cy.Cmp(b.Cy) == 0 && a.R2.Cmp(b.R2) == 0
}

func build(t *testing.T, pts ...api.Point) *api.Service {
	t.Helper()
	s, _ := api.New()
	for _, p := range pts {
		if err := s.Insert(p.X, p.Y); err != nil {
			t.Fatalf("insert %v: %v", p, err)
		}
	}
	return s
}

var fiveSets = []struct {
	pts        []api.Point
	cx, cy, r2 string
	nb         int
}{
	{[]api.Point{pt(0, 0)}, "0", "0", "0", 1},
	{[]api.Point{pt(0, 0), pt(6, 0)}, "3", "0", "9", 2},
	{[]api.Point{pt(0, 0), pt(6, 0), pt(0, 8)}, "3", "4", "25", 3},
	{[]api.Point{pt(0, 0), pt(6, 0), pt(1, 1)}, "3", "0", "9", 2},
	{[]api.Point{pt(0, 0), pt(3, 0), pt(6, 0)}, "3", "0", "9", 2},
}

// 第三节五个点集：圆心、半径平方、边界点数逐一钉死。
func TestFivePointSets(t *testing.T) {
	for i, tc := range fiveSets {
		s := build(t, tc.pts...)
		c, err := s.MinCircle()
		if err != nil || !circleEq(c, tc.cx, tc.cy, tc.r2) || len(s.Boundary()) != tc.nb {
			t.Errorf("set %d: %v,%v,%v nb=%d err=%v", i, c.Cx, c.Cy, c.R2, len(s.Boundary()), err)
		}
	}
}

// 三类哨兵错误可判定且互不相同；边界坐标 ±10000 合法。
func TestSentinelErrors(t *testing.T) {
	s, _ := api.New()
	if _, err := s.MinCircle(); !errors.Is(err, api.ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if err := s.Insert(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(0, 0); !errors.Is(err, api.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	for _, bad := range [][2]int{{10001, 0}, {-10001, 0}, {0, 10001}, {0, -10001}} {
		if err := s.Insert(bad[0], bad[1]); !errors.Is(err, api.ErrOutOfRange) {
			t.Fatalf("range %v: %v", bad, err)
		}
	}
	if errors.Is(api.ErrDuplicate, api.ErrOutOfRange) || errors.Is(api.ErrEmpty, api.ErrDuplicate) ||
		errors.Is(api.ErrEmpty, api.ErrOutOfRange) {
		t.Fatal("sentinels must be mutually distinct")
	}
	for _, ok := range [][2]int{{10000, 10000}, {-10000, -10000}} {
		if err := s.Insert(ok[0], ok[1]); err != nil {
			t.Fatalf("boundary coord %v: %v", ok, err)
		}
	}
}

// 不变量 4：被拒操作不改变任何状态，服务仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := build(t, pt(0, 0), pt(6, 0), pt(0, 8))
	before, _ := s.MinCircle()
	bb := s.Boundary()
	_ = s.Insert(0, 0)
	_ = s.Insert(10001, 0)
	_ = s.Insert(0, -10001)
	after, _ := s.MinCircle()
	if !sameCircle(before, after) || !slices.Equal(bb, s.Boundary()) {
		t.Fatal("state changed after rejected ops")
	}
	if err := s.Insert(3, 3); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
	s2, _ := api.New()
	if _, err := s2.MinCircle(); !errors.Is(err, api.ErrEmpty) {
		t.Fatal(err)
	}
	if err := s2.Insert(1, 1); err != nil {
		t.Fatalf("empty-query left trace: %v", err)
	}
}

// 并发只读：多 goroutine 同时读，圆心、r²、边界逐字段相同（-race 下验证）。
func TestConcurrentReaders(t *testing.T) {
	s := build(t, pt(0, 0), pt(6, 0), pt(0, 8), pt(-3, -4), pt(10, 2))
	want, _ := s.MinCircle()
	wantB := s.Boundary()
	var wg sync.WaitGroup
	var bad atomic.Bool
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				c, err := s.MinCircle()
				if err != nil || !sameCircle(c, want) || !slices.Equal(s.Boundary(), wantB) || s.SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent read mismatch")
	}
}

// 内置点集四条不变量自检。
func TestSelfCheck(t *testing.T) {
	s, _ := api.New()
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
