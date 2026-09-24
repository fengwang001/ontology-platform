package sched

import (
	"container/heap"
	"fmt"
	"math"
	"testing"

	"ontology/task"
	"ontology/tenant"
)

func newSched(weights map[string]float64) *Scheduler {
	s := New()
	for id, w := range weights {
		s.Add(tenant.New(id, w))
	}
	return s
}

func fill(s *Scheduler, id string, n int, cost float64) {
	for i := 0; i < n; i++ {
		s.Submit(task.Task{Tenant: id, Cost: cost, Seq: uint64(i)})
	}
}

func drain(s *Scheduler, steps int) []string {
	var out []string
	for i := 0; i < steps; i++ {
		tk, ok := s.Next()
		if !ok {
			break
		}
		out = append(out, tk.Tenant)
	}
	return out
}

func counts(seq []string) map[string]int {
	m := map[string]int{}
	for _, id := range seq {
		m[id]++
	}
	return m
}

// 同 vt 并列：任意提交顺序都按租户 ID 字典序出队。
func TestTieBreak(t *testing.T) {
	for _, order := range [][]string{{"a", "b", "c"}, {"c", "b", "a"}, {"b", "a", "c"}} {
		s := New()
		for _, id := range order {
			s.Add(tenant.New(id, 1))
			s.Submit(task.Task{Tenant: id, Cost: 1})
		}
		for i, want := range []string{"a", "b", "c"} {
			if got := drain(s, 1)[0]; got != want {
				t.Errorf("submit %v: pick %d = %s, want %s", order, i, got, want)
			}
		}
	}
}

// 单次选择比较次数 <= 4*ceil(log2 n)；堆中元素数 == 非空租户数。
func TestComparisonBound(t *testing.T) {
	for _, c := range []struct{ n, bound int }{{10, 16}, {100, 28}, {1000, 40}} {
		s := New()
		for i := 0; i < c.n; i++ {
			id := fmt.Sprintf("t%04d", i)
			s.Add(tenant.New(id, 1))
			fill(s, id, 2, 1)
		}
		s.Next()
		if got := s.LastComparisons(); got > c.bound {
			t.Errorf("n=%d: cmps=%d > bound %d", c.n, got, c.bound)
		}
		if s.Active() != c.n {
			t.Errorf("n=%d: active=%d, want %d", c.n, s.Active(), c.n)
		}
	}
	s := New()
	for i := 0; i < 1000; i++ {
		s.Add(tenant.New(fmt.Sprintf("t%04d", i), 1))
	}
	for i := 0; i < 37; i++ {
		fill(s, fmt.Sprintf("t%04d", i), 1, 1)
	}
	if s.Active() != 37 {
		t.Errorf("idle tenants in heap: active=%d, want 37", s.Active())
	}
}

// 空闲重入：抬升 vt 则几步内被服务且不独占；不抬升则连续独占约 100 步。
func TestIdleRejoin(t *testing.T) {
	for _, c := range []struct {
		name                               string
		lift                               bool
		nX                                 int
		consecLo, consecHi, firstXHi, minY int
	}{
		{"抬升/单任务", true, 1, 1, 1, 3, 150},
		{"抬升/百任务", true, 100, 1, 2, 3, 90},
		{"不抬升/百任务", false, 100, 100, 200, 3, 0},
	} {
		s := newSched(map[string]float64{"X": 1, "Y": 1})
		fill(s, "Y", 1200, 1)
		drain(s, 1000) // X 空闲期间 Y 执行 1000 步
		if c.lift {
			fill(s, "X", c.nX, 1)
		} else { // 对照：绕过 LiftVT 直接入堆
			x := s.Tenant("X")
			for i := 0; i < c.nX; i++ {
				x.Enqueue(task.Task{Tenant: "X", Cost: 1})
			}
			heap.Push(&s.h, x)
		}
		seq := drain(s, 200)
		consec, cur, firstX := 0, 0, -1
		for i, id := range seq {
			if id == "X" {
				cur++
				if cur > consec {
					consec = cur
				}
				if firstX < 0 {
					firstX = i
				}
			} else {
				cur = 0
			}
		}
		y := counts(seq)["Y"]
		if consec < c.consecLo || consec > c.consecHi || firstX > c.firstXHi || y < c.minY {
			t.Errorf("%s: consec=%d firstX=%d y=%d", c.name, consec, firstX, y)
		}
	}
}

// 零/负代价任务被 MinCost 下界兜住，不会无限独占。
func TestZeroCostNoMonopoly(t *testing.T) {
	for _, cost := range []float64{0, -2, 1} {
		s := newSched(map[string]float64{"W": 1, "Z": 1})
		fill(s, "Z", 50, cost)
		fill(s, "W", 50, 1)
		if got := counts(drain(s, 100))["W"]; got < 40 {
			t.Errorf("cost=%v: W picked %d < 40, Z monopolizes", cost, got)
		}
	}
}

// 权重极大与极小并存：虚拟时间不溢出。
func TestExtremeWeights(t *testing.T) {
	s := newSched(map[string]float64{"big": 1e6, "tiny": 1e-6})
	fill(s, "big", 2000, 1)
	fill(s, "tiny", 2000, 1e6)
	drain(s, 2000)
	for _, id := range []string{"big", "tiny"} {
		if vt := s.Tenant(id).VT(); math.IsNaN(vt) || math.IsInf(vt, 0) {
			t.Errorf("%s vt overflow: %v", id, vt)
		}
	}
}
