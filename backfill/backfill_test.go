package backfill

import (
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 相交判定的前驱与后继两种情形，且 cmps <= 2。
func TestOverlapPredecessorAndSuccessor(t *testing.T) {
	m := NewManager(10)
	mustOK(t, m.Begin("lo", 10, 12, 100, 0))
	mustOK(t, m.Begin("hi", 20, 22, 100, 0))

	// 与前驱相交：冲突者取起点最小者。
	err := m.Begin("x", 12, 15, 100, 0)
	var oe *OverlapError
	if !errors.As(err, &oe) || oe.Conflict != "lo" {
		t.Fatalf("err = %v, want ErrOverlap(lo)", err)
	}
	if m.cmps > 2 {
		t.Fatalf("cmps = %d, want <= 2", m.cmps)
	}

	// 与后继相交。
	err = m.Begin("y", 18, 20, 100, 0)
	if !errors.As(err, &oe) || oe.Conflict != "hi" {
		t.Fatalf("err = %v, want ErrOverlap(hi)", err)
	}
	if m.cmps > 2 {
		t.Fatalf("cmps = %d, want <= 2", m.cmps)
	}

	// 同时与两者相交：报起点最小的冲突者（前驱）。
	err = m.Begin("z", 11, 21, 100, 0)
	if !errors.As(err, &oe) || oe.Conflict != "lo" {
		t.Fatalf("err = %v, want ErrOverlap(lo)", err)
	}

	// 落在空隙中：成功。
	mustOK(t, m.Begin("mid", 14, 17, 100, 0))
	if m.cmps > 2 {
		t.Fatalf("cmps = %d, want <= 2", m.cmps)
	}
}

// cmps 界：活跃作业数 2 与 J 两档，单次 Begin 相交判定比较不超过 2。
func TestCmpsBound(t *testing.T) {
	for _, jobs := range []int{2, 64} {
		m := NewManager(jobs + 1)
		for i := 0; i < jobs; i++ {
			a := i * 10
			mustOK(t, m.Begin(string(rune('A'+i)), a, a+5, 100, 0))
		}
		// 与最后一个作业相交。
		lastA := (jobs - 1) * 10
		err := m.Begin("probe", lastA+3, lastA+6, 100, 0)
		if !errors.Is(err, ErrOverlap) {
			t.Fatalf("jobs=%d: err = %v, want ErrOverlap", jobs, err)
		}
		if m.cmps > 2 {
			t.Fatalf("jobs=%d: cmps = %d, want <= 2", jobs, m.cmps)
		}
		// 不相交的成功 Begin 同样不超过 2。
		mustOK(t, m.Begin("free", 7, 9, 100, 0))
		if m.cmps > 2 {
			t.Fatalf("jobs=%d: cmps = %d, want <= 2", jobs, m.cmps)
		}
	}
}

// 区间端点相接不算相交：[a,b] 与 [b+1,c] 可共存。
func TestAdjacentIntervals(t *testing.T) {
	m := NewManager(10)
	mustOK(t, m.Begin("a", 0, 1, 100, 0))
	mustOK(t, m.Begin("b", 2, 3, 100, 0))
	mustOK(t, m.Begin("c", 4, 4, 100, 0))
	if err := m.Begin("d", 1, 2, 100, 0); !errors.Is(err, ErrOverlap) {
		t.Fatalf("err = %v, want ErrOverlap", err)
	}
}

// 恰等到期即过期；小 1 仍活跃；过期后同名可重新 Begin，旧暂存不生效。
func TestExpiryLazyAndRestage(t *testing.T) {
	m := NewManager(10)
	mustOK(t, m.Begin("j", 0, 1, 5, 10)) // deadline = 15
	mustOK(t, m.Stage("j", 0, 11))
	mustOK(t, m.Stage("j", 1, 12))

	// now = deadline-1：仍活跃。
	if m.Lookup("j", 14) == nil {
		t.Fatal("job should be active at deadline-1")
	}
	if _, ok := m.Holder(0, 14); !ok {
		t.Fatal("partition 0 should be held at deadline-1")
	}
	// now = deadline：视同不存在，但取消未落地前仍占用已落地视图。
	if m.Lookup("j", 15) != nil {
		t.Fatal("job should be treated as nonexistent at deadline")
	}
	if _, ok := m.Holder(0, 15); ok {
		t.Fatal("partition 0 should not be held at deadline")
	}
	if a, ok := m.MinStartLanded(); !ok || a != 0 {
		t.Fatal("expired job should still depress landed view before sweep")
	}
	// 被拒操作不落地取消：ErrIncomplete 的 Finish 不改变已落地视图。
	if _, err := m.Finish("j", 15); !errors.Is(err, ErrNoJob) {
		t.Fatalf("Finish err = %v, want ErrNoJob", err)
	}
	if a, ok := m.MinStartLanded(); !ok || a != 0 {
		t.Fatal("rejected op must not land the cancellation")
	}
	// 被接受的操作落地取消。
	mustOK(t, m.Begin("other", 10, 11, 5, 15))
	if _, ok := m.MinStartLanded(); !ok {
		t.Fatal("expected landed jobs")
	}
	if m.Lookup("j", 15) != nil || m.byName["j"] != nil {
		t.Fatal("expired job should be swept after accepted op")
	}
	// 同名重新 Begin：旧暂存不生效，Finish 报 ErrIncomplete(0)。
	mustOK(t, m.Begin("j", 0, 1, 5, 16))
	var ie *IncompleteError
	if _, err := m.Finish("j", 17); !errors.As(err, &ie) || ie.Part != 0 {
		t.Fatalf("Finish err = %v, want ErrIncomplete(0)", err)
	}
}

func TestStageHeartbeatFinishAbort(t *testing.T) {
	m := NewManager(2)
	mustOK(t, m.Begin("j", 5, 7, 10, 0))

	// Stage 越界与幂等。
	if err := m.Stage("j", 8, 1); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
	mustOK(t, m.Stage("j", 5, 1))
	mustOK(t, m.Stage("j", 5, 2)) // 幂等
	mustOK(t, m.Stage("j", 6, 2))

	// Stage 不续期：deadline 仍为 10。
	if m.Lookup("j", 10) != nil {
		t.Fatal("Stage must not renew deadline")
	}
	// Heartbeat 续期：deadline = now + ttl。
	mustOK(t, m.Heartbeat("j", 3))
	if j := m.Lookup("j", 3); j == nil || j.Deadline != 13 {
		t.Fatalf("deadline = %v, want 13", m.byName["j"].Deadline)
	}

	// Finish 缺分区：报最小未暂存号。
	var ie *IncompleteError
	if _, err := m.Finish("j", 4); !errors.As(err, &ie) || ie.Part != 7 {
		t.Fatalf("Finish err = %v, want ErrIncomplete(7)", err)
	}
	mustOK(t, m.Stage("j", 7, 4))
	j, err := m.Finish("j", 5)
	mustOK(t, err)
	if j.A != 5 || j.B != 7 {
		t.Fatalf("finished job = %+v", j)
	}
	if m.Lookup("j", 5) != nil {
		t.Fatal("job should be gone after Finish")
	}

	// Abort 丢弃暂存并释放区间。
	mustOK(t, m.Begin("k", 0, 2, 10, 6))
	mustOK(t, m.Stage("k", 0, 6))
	mustOK(t, m.Abort("k", 7))
	if _, ok := m.Holder(1, 7); ok {
		t.Fatal("interval should be released after Abort")
	}
	if err := m.Heartbeat("k", 7); !errors.Is(err, ErrNoJob) {
		t.Fatalf("err = %v, want ErrNoJob", err)
	}
}

func TestBeginLimits(t *testing.T) {
	m := NewManager(1)
	mustOK(t, m.Begin("j", 0, 1, 10, 0))
	// 已存在优先于 ErrTooManyJobs。
	if err := m.Begin("j", 5, 6, 10, 0); !errors.Is(err, ErrJobExists) {
		t.Fatalf("err = %v, want ErrJobExists", err)
	}
	// 满员优先于 ErrOverlap。
	if err := m.Begin("k", 0, 1, 10, 0); !errors.Is(err, ErrTooManyJobs) {
		t.Fatalf("err = %v, want ErrTooManyJobs", err)
	}
}
