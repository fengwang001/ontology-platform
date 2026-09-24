package stat_test

import (
	"testing"

	"ontology/sched"
	"ontology/stat"
	"ontology/task"
	"ontology/tenant"
)

func newSched(weights map[string]float64) *sched.Scheduler {
	s := sched.New()
	for id, w := range weights {
		s.Add(tenant.New(id, w))
	}
	return s
}

func fill(s *sched.Scheduler, id string, n int, cost float64) {
	for i := 0; i < n; i++ {
		s.Submit(task.Task{Tenant: id, Cost: cost, Seq: uint64(i)})
	}
}

// Meter 的占比/偏差计算：逐租户表驱动断言。
func TestShares(t *testing.T) {
	m := stat.New()
	if m.Share("a") != 0 || m.Want("a") != 0 {
		t.Fatal("empty meter must report 0")
	}
	m.SetWeight("a", 1)
	m.SetWeight("b", 3)
	m.Record("a", 30)
	m.Record("b", 70)
	for _, c := range []struct {
		id        string
		share     float64
		want      float64
		deviation float64
	}{
		{"a", 0.3, 0.25, 0.2},
		{"b", 0.7, 0.75, 1.0 / 15.0},
	} {
		if got := m.Share(c.id); got != c.share {
			t.Errorf("%s share=%v, want %v", c.id, got, c.share)
		}
		if got := m.Want(c.id); got != c.want {
			t.Errorf("%s want=%v, want %v", c.id, got, c.want)
		}
		if got := m.RelDeviation(c.id); got < c.deviation-1e-9 || got > c.deviation+1e-9 {
			t.Errorf("%s dev=%v, want %v", c.id, got, c.deviation)
		}
	}
	if id, d := m.MaxRelDeviation(); id != "a" || d < 0.2-1e-9 {
		t.Errorf("max dev = (%s, %v), want (a, 0.2)", id, d)
	}
}

// 权重 1:3 等代价任务：长期执行次数比例收敛到 1:3（±5%）。
func TestConvergence(t *testing.T) {
	for _, c := range []struct {
		wA, wB, ratio float64
	}{
		{1, 3, 3},
		{2, 5, 2.5},
	} {
		s := newSched(map[string]float64{"A": c.wA, "B": c.wB})
		fill(s, "A", 60000, 1)
		fill(s, "B", 60000, 1)
		got := map[string]int{}
		for i := 0; i < 60000; i++ {
			tk, _ := s.Next()
			got[tk.Tenant]++
		}
		r := float64(got["B"]) / float64(got["A"])
		if d := r/c.ratio - 1; d < -0.05 || d > 0.05 {
			t.Errorf("w=%v:%v ratio=%.4f, want %v ±5%%", c.wA, c.wB, r, c.ratio)
		}
	}
}

// 权重 1:3:6 跑 10 万次调度：逐租户实际代价占比与权重占比相对偏差 < 5%。
func TestFairness100k(t *testing.T) {
	weights := map[string]float64{"p": 1, "q": 3, "r": 6}
	s := newSched(weights)
	for id := range weights {
		fill(s, id, 100000, 1)
	}
	m := stat.New()
	for id, w := range weights {
		m.SetWeight(id, w)
	}
	for i := 0; i < 100000; i++ {
		tk, _ := s.Next()
		m.Record(tk.Tenant, tk.Cost)
	}
	for id := range weights {
		if d := m.RelDeviation(id); d >= 0.05 {
			t.Errorf("%s deviation=%.4f >= 5%% (share=%.4f want=%.4f)",
				id, d, m.Share(id), m.Want(id))
		}
	}
}
