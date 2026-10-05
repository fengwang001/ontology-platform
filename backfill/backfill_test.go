package backfill

import (
	"errors"
	"testing"
)

func mustBegin(t *testing.T, m *Manager, name string, a, b, ttl, now int) {
	t.Helper()
	if err := m.Begin(name, a, b, ttl, now); err != nil {
		t.Fatalf("Begin(%q,%d,%d) = %v", name, a, b, err)
	}
}

// 相交判定：冲突者起点最小时是前驱，否则是后继；两种情形都要命中。
func TestOverlapPredecessorAndSuccessor(t *testing.T) {
	t.Run("predecessor", func(t *testing.T) {
		m := NewManager(8)
		mustBegin(t, m, "j1", 10, 12, 5, 0)
		mustBegin(t, m, "j2", 20, 22, 5, 0)
		err := m.Begin("j3", 8, 10, 5, 0) // 与前驱 j1 端点相接于 10
		var be *Error
		if !errors.As(err, &be) || !errors.Is(err, ErrOverlap) || be.Job != "j1" {
			t.Fatalf("err = %v, want ErrOverlap(j1)", err)
		}
	})
	t.Run("successor", func(t *testing.T) {
		m := NewManager(8)
		mustBegin(t, m, "j1", 10, 12, 5, 0)
		mustBegin(t, m, "j2", 20, 22, 5, 0)
		err := m.Begin("j3", 13, 20, 5, 0) // 前驱 j1 不相交，后继 j2 相交于 20
		var be *Error
		if !errors.As(err, &be) || !errors.Is(err, ErrOverlap) || be.Job != "j2" {
			t.Fatalf("err = %v, want ErrOverlap(j2)", err)
		}
	})
	t.Run("minStartConflictor", func(t *testing.T) {
		m := NewManager(8)
		mustBegin(t, m, "j1", 10, 12, 5, 0)
		mustBegin(t, m, "j2", 20, 22, 5, 0)
		err := m.Begin("j3", 11, 21, 5, 0) // 同时与 j1、j2 相交，报起点最小的 j1
		var be *Error
		if !errors.As(err, &be) || be.Job != "j1" {
			t.Fatalf("err = %v, want ErrOverlap(j1)", err)
		}
	})
}

// 区间端点相接（b+1 == a）不算相交。
func TestAdjacentIntervals(t *testing.T) {
	m := NewManager(8)
	mustBegin(t, m, "j1", 1, 2, 5, 0)
	mustBegin(t, m, "j2", 3, 4, 5, 0) // [1,2] 与 [3,4] 相接不相交
	mustBegin(t, m, "j3", 0, 0, 5, 0)
	if err := m.Begin("j4", 2, 3, 5, 0); !errors.Is(err, ErrOverlap) {
		t.Fatalf("Begin overlapping both = %v, want ErrOverlap", err)
	}
}

// cmps 上界：Begin 相交判定比较的活跃作业数不超过 2。活跃作业数 2 与 J 两档。
func TestCmpsBound(t *testing.T) {
	for _, jobs := range []int{2, 1000} {
		t.Run("", func(t *testing.T) {
			m := NewManager(jobs + 1)
			for i := 0; i < jobs; i++ {
				name := string(rune('a'+i%26)) + string(rune('A'+i/26))
				mustBegin(t, m, name, i*10, i*10+5, 1000, 0)
			}
			before := m.Cmps()
			mid := jobs / 2
			err := m.Begin("x", mid*10+2, mid*10+3, 1000, 0) // 必相交
			if !errors.Is(err, ErrOverlap) {
				t.Fatalf("err = %v, want ErrOverlap", err)
			}
			if got := m.Cmps() - before; got > 2 {
				t.Fatalf("cmps delta = %d, want <= 2 (jobs=%d)", got, jobs)
			}
			before = m.Cmps()
			mustBegin(t, m, "y", jobs*10+100, jobs*10+101, 1000, 0) // 不相交
			if got := m.Cmps() - before; got > 2 {
				t.Fatalf("cmps delta = %d, want <= 2 (jobs=%d)", got, jobs)
			}
		})
	}
}

// 拒绝次序：ErrJobExists > ErrTooManyJobs > ErrOverlap。
func TestBeginRejectionOrder(t *testing.T) {
	m := NewManager(1)
	mustBegin(t, m, "j1", 0, 5, 100, 0)
	if err := m.Begin("j1", 10, 11, 100, 0); !errors.Is(err, ErrJobExists) {
		t.Fatalf("dup name = %v, want ErrJobExists", err)
	}
	// 容量已满：即使区间相交也先报 ErrTooManyJobs。
	if err := m.Begin("j2", 2, 3, 100, 0); !errors.Is(err, ErrTooManyJobs) {
		t.Fatalf("full+overlap = %v, want ErrTooManyJobs", err)
	}
	m2 := NewManager(2)
	mustBegin(t, m2, "j1", 0, 5, 100, 0)
	if err := m2.Begin("j2", 3, 8, 100, 0); !errors.Is(err, ErrOverlap) {
		t.Fatalf("overlap = %v, want ErrOverlap", err)
	}
}

// 过期作业视同不存在：同名可重新 Begin，区间不再占用；Sweep 后落地。
func TestExpiryAndSweep(t *testing.T) {
	m := NewManager(4)
	mustBegin(t, m, "j1", 0, 5, 10, 0) // deadline = 10
	if _, held := m.Holder(3, 9); !held {
		t.Fatal("p=3 should be held at now=9")
	}
	if _, held := m.Holder(3, 10); held {
		t.Fatal("p=3 should not be held at now=10 (expired)")
	}
	if j := m.Get("j1", 10); j != nil {
		t.Fatal("Get at deadline should be nil (expired)")
	}
	// 恰等 deadline 即过期，同名可重新 Begin。
	mustBegin(t, m, "j1", 0, 5, 10, 10)
	// 未落地前旧作业仍占排序位；Sweep 后只剩新作业。
	m.Sweep(10)
	if got := len(m.sorted); got != 1 {
		t.Fatalf("len(sorted) = %d, want 1", got)
	}
	if amin, ok := m.MinStartLanded(); !ok || amin != 0 {
		t.Fatalf("MinStartLanded = %d,%v, want 0,true", amin, ok)
	}
}

// 过期但未落地的作业仍计入 MinStartLanded（S 只反映已落地状态）。
func TestMinStartLandedIncludesExpired(t *testing.T) {
	m := NewManager(4)
	mustBegin(t, m, "j1", 5, 6, 3, 0)    // deadline = 3，已过期
	mustBegin(t, m, "j2", 9, 10, 100, 4) // 接受时落地 j1 的取消
	if amin, ok := m.MinStartLanded(); !ok || amin != 9 {
		t.Fatalf("MinStartLanded = %d,%v, want 9,true", amin, ok)
	}
}

func TestJobStaging(t *testing.T) {
	m := NewManager(2)
	mustBegin(t, m, "j1", 2, 4, 10, 0)
	j := m.Get("j1", 0)
	if p, ok := j.FirstUnstaged(); !ok || p != 2 {
		t.Fatalf("FirstUnstaged = %d,%v, want 2,true", p, ok)
	}
	j.Stage(2)
	j.Stage(2) // 幂等
	j.Stage(4)
	if p, ok := j.FirstUnstaged(); !ok || p != 3 {
		t.Fatalf("FirstUnstaged = %d,%v, want 3,true", p, ok)
	}
	j.Stage(3)
	if _, ok := j.FirstUnstaged(); ok {
		t.Fatal("should be fully staged")
	}
	m.Remove(j)
	if j := m.Get("j1", 0); j != nil {
		t.Fatal("removed job should be nil")
	}
}
