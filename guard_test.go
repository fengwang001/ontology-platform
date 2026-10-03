package ontology

import (
	"errors"
	"testing"

	"ontology/breaker"
)

// exampleParams 为题面示例参数。
func exampleParams() Params {
	return Params{N: 2, M: 2, F: 50, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 1, Q: 1}
}

func mustAcquire(t *testing.T, g *Guard, now int64, want AcquireOutcome) AcquireResult {
	t.Helper()
	res, err := g.Acquire(now)
	if err != nil {
		t.Fatalf("Acquire(%d) unexpected error: %v", now, err)
	}
	if res.Outcome != want {
		t.Fatalf("Acquire(%d) outcome=%v want %v", now, res.Outcome, want)
	}
	return res
}

func mustRelease(t *testing.T, g *Guard, id int64, ok bool, dur, now int64) {
	t.Helper()
	if err := g.Release(id, ok, dur, now); err != nil {
		t.Fatalf("Release(%d,%v,%d,%d) unexpected error: %v", id, ok, dur, now, err)
	}
}

func tripOpen(t *testing.T, g *Guard) {
	t.Helper()
	mustAcquire(t, g, 0, Granted)
	mustAcquire(t, g, 1, Queued)
	mustRelease(t, g, 1, true, 10, 3)
	mustRelease(t, g, 2, false, 10, 4)
}

// TestExampleFromSpec 复现题面完整示例（排队授予、恰等阈值开路、半开探测恢复）。
func TestExampleFromSpec(t *testing.T) {
	g, err := New(exampleParams())
	if err != nil {
		t.Fatal(err)
	}
	r1 := mustAcquire(t, g, 0, Granted)
	if r1.ID != 1 {
		t.Fatalf("first id=%d", r1.ID)
	}
	r2 := mustAcquire(t, g, 1, Queued)
	if r2.ID != 2 {
		t.Fatalf("second id=%d", r2.ID)
	}
	mustAcquire(t, g, 2, RejectedFull)

	mustRelease(t, g, 1, true, 10, 3)
	if st, _ := g.Status(2, 3); st != StatusActive {
		t.Fatalf("id2 should be granted from queue, got %v", st)
	}
	mustRelease(t, g, 2, false, 10, 4)
	snap, _ := g.Snapshot(4)
	if snap.State != breaker.Open || snap.Epoch != 1 || snap.Count != 0 || snap.Queued != 0 {
		t.Fatalf("after trip: %+v", snap)
	}

	mustAcquire(t, g, 13, RejectedOpen)
	r3 := mustAcquire(t, g, 14, Granted)
	if r3.ID != 3 {
		t.Fatalf("probe id=%d", r3.ID)
	}
	snap, _ = g.Snapshot(14)
	if snap.State != breaker.HalfOpen || snap.Epoch != 2 {
		t.Fatalf("half open: %+v", snap)
	}
	mustAcquire(t, g, 14, RejectedHalfOpenFull)
	mustRelease(t, g, 3, true, 10, 15)
	snap, _ = g.Snapshot(15)
	if snap.State != breaker.Closed || snap.Epoch != 3 {
		t.Fatalf("recovered: %+v", snap)
	}
}

// TestQueueTimeoutAtExactBoundary 排队恰在 enqueuedAt+Wt 超时。
func TestQueueTimeoutAtExactBoundary(t *testing.T) {
	g, _ := New(exampleParams())
	mustAcquire(t, g, 0, Granted)
	q := mustAcquire(t, g, 1, Queued)
	mustRelease(t, g, 1, true, 10, 6) // now=6=1+5，结算先超时
	st, err := g.Status(q.ID, 6)
	if err != nil || st != StatusTimedOut {
		t.Fatalf("id2 status=%v err=%v, want timed out", st, err)
	}
	snap, _ := g.Snapshot(6)
	if snap.Active != 0 || snap.Queued != 0 {
		t.Fatalf("after timeout: %+v", snap)
	}
}

// TestOpenCancelsQueuedInOrder 开路时排队者全部按序撤销。
func TestOpenCancelsQueuedInOrder(t *testing.T) {
	p := exampleParams()
	p.C, p.Q = 1, 3
	g, _ := New(p)
	mustAcquire(t, g, 0, Granted)
	q2 := mustAcquire(t, g, 1, Queued)
	q3 := mustAcquire(t, g, 2, Queued)
	q4 := mustAcquire(t, g, 3, Queued)
	mustRelease(t, g, 1, false, 10, 4) // 条数 1 < M，仍 Closed
	if st, _ := g.Status(q2.ID, 4); st != StatusActive {
		t.Fatalf("q2 should be granted on release, got %v", st)
	}
	if snap, _ := g.Snapshot(4); snap.State != breaker.Closed || snap.Queued != 2 {
		t.Fatalf("pre: %+v", snap)
	}
	// id2 已在 now=4 获授，失败归还 -> 环两条失败 -> 开路，撤销 id3 id4。
	mustRelease(t, g, q2.ID, false, 10, 5)
	for _, id := range []int64{q3.ID, q4.ID} {
		if st, _ := g.Status(id, 5); st != StatusCancelled {
			t.Fatalf("id=%d status=%v want cancelled", id, st)
		}
	}
	snap, _ := g.Snapshot(5)
	if snap.State != breaker.Open || snap.Queued != 0 || snap.Active != 0 {
		t.Fatalf("after trip: %+v", snap)
	}
}

// TestHalfOpenFailuresReopen 半开探测失败或慢（dur 恰等于 S）均重新开路。
func TestHalfOpenFailuresReopen(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
		dur  int64
	}{
		{"failed", false, 10},
		{"slow", true, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := New(exampleParams())
			tripOpen(t, g)
			mustAcquire(t, g, 13, RejectedOpen)
			r3 := mustAcquire(t, g, 14, Granted)
			mustRelease(t, g, r3.ID, tc.ok, tc.dur, 16)
			snap, _ := g.Snapshot(16)
			if snap.State != breaker.Open || snap.Epoch != 3 {
				t.Fatalf("%s: %+v", tc.name, snap)
			}
			mustAcquire(t, g, 25, RejectedOpen)
			mustAcquire(t, g, 26, Granted)
		})
	}
}

// TestFailureRateBoundary 失败率恰等于阈值开路，差 1 不开路。
func TestFailureRateBoundary(t *testing.T) {
	newG := func(f int) *Guard {
		g, err := New(Params{N: 2, M: 2, F: f, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 10, Q: 0})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	g := newG(50)
	a := mustAcquire(t, g, 0, Granted).ID
	b := mustAcquire(t, g, 0, Granted).ID
	mustRelease(t, g, a, true, 10, 1)
	mustRelease(t, g, b, false, 10, 2)
	if snap, _ := g.Snapshot(2); snap.State != breaker.Open {
		t.Fatalf("equal threshold must trip: %+v", snap)
	}

	g2 := newG(51)
	a = mustAcquire(t, g2, 0, Granted).ID
	b = mustAcquire(t, g2, 0, Granted).ID
	mustRelease(t, g2, a, true, 10, 1)
	mustRelease(t, g2, b, false, 10, 2)
	if snap, _ := g2.Snapshot(2); snap.State != breaker.Closed {
		t.Fatalf("below threshold must stay closed: %+v", snap)
	}
}

// TestSlowRateAloneTriggers 慢调用（dur 恰等于 S）单独触发开路。
func TestSlowRateAloneTriggers(t *testing.T) {
	g, _ := New(Params{N: 2, M: 2, F: 100, SR: 50, S: 100, O: 10, Wt: 5, H: 1, C: 10, Q: 0})
	a := mustAcquire(t, g, 0, Granted).ID
	b := mustAcquire(t, g, 0, Granted).ID
	mustRelease(t, g, a, true, 99, 1)
	mustRelease(t, g, b, true, 100, 2)
	snap, _ := g.Snapshot(2)
	if snap.State != breaker.Open {
		t.Fatalf("slow alone must trip: %+v", snap)
	}
}

// TestBelowMNoJudgement 条数未到 M 不判定。
func TestBelowMNoJudgement(t *testing.T) {
	g, _ := New(Params{N: 2, M: 2, F: 50, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 10, Q: 0})
	a := mustAcquire(t, g, 0, Granted).ID
	mustRelease(t, g, a, false, 10, 1)
	if snap, _ := g.Snapshot(1); snap.State != breaker.Closed || snap.Count != 1 || snap.Failed != 1 {
		t.Fatalf("below M: %+v", snap)
	}
}

// TestRingEvictionDropsOldest 环满淘汰最旧，计数随淘汰更新后触发开路。
func TestRingEvictionDropsOldest(t *testing.T) {
	g, _ := New(Params{N: 2, M: 2, F: 50, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 10, Q: 0})
	ids := []int64{
		mustAcquire(t, g, 0, Granted).ID,
		mustAcquire(t, g, 0, Granted).ID,
		mustAcquire(t, g, 0, Granted).ID,
	}
	mustRelease(t, g, ids[0], true, 10, 1)
	mustRelease(t, g, ids[1], true, 10, 2)
	mustRelease(t, g, ids[2], false, 10, 3)
	if snap, _ := g.Snapshot(3); snap.State != breaker.Open || snap.Count != 0 {
		t.Fatalf("evicted window must trip: %+v", snap)
	}
}

// TestHalfOpenMultiProbe H=3：探测数用尽拒绝，第三个成功后恢复。
func TestHalfOpenMultiProbe(t *testing.T) {
	p := exampleParams()
	p.H, p.C = 3, 3
	g, _ := New(p)
	// 无排队（Q=0）：两条失败（恰阈 50%）触发开路。
	x1 := mustAcquire(t, g, 0, Granted).ID
	x2 := mustAcquire(t, g, 0, Granted).ID
	mustRelease(t, g, x1, false, 10, 1)
	mustRelease(t, g, x2, false, 10, 2)
	if snap, _ := g.Snapshot(4); snap.State != breaker.Open {
		t.Fatalf("setup trip: %+v", snap)
	}
	probes := []int64{
		mustAcquire(t, g, 14, Granted).ID,
		mustAcquire(t, g, 14, Granted).ID,
		mustAcquire(t, g, 14, Granted).ID,
	}
	mustAcquire(t, g, 14, RejectedHalfOpenFull)
	mustRelease(t, g, probes[0], true, 10, 15)
	mustRelease(t, g, probes[1], true, 10, 16)
	if snap, _ := g.Snapshot(16); snap.State != breaker.HalfOpen {
		t.Fatalf("still half open: %+v", snap)
	}
	mustRelease(t, g, probes[2], true, 10, 17)
	if snap, _ := g.Snapshot(17); snap.State != breaker.Closed || snap.Epoch != 3 {
		t.Fatalf("recovered after H probes: %+v", snap)
	}
}

// TestStaleEpochReleaseIgnored 旧纪元许可晚归还不污染环。
func TestStaleEpochReleaseIgnored(t *testing.T) {
	p := exampleParams()
	p.C, p.Q = 2, 0
	g, _ := New(p)
	hold := mustAcquire(t, g, 0, Granted).ID // epoch0 发放，长期不还
	x := mustAcquire(t, g, 0, Granted).ID
	mustRelease(t, g, x, false, 10, 1) // 环 1 条 <M，未开路
	y := mustAcquire(t, g, 1, Granted).ID
	mustRelease(t, g, y, false, 10, 2) // 两条失败 -> 开路（hold 仍在役）
	mustAcquire(t, g, 2, RejectedOpen)
	pid := mustAcquire(t, g, 12, Granted).ID // openedAt=2，2+10=12 转 HalfOpen
	mustRelease(t, g, pid, true, 10, 13)     // 回 Closed（epoch3），环空
	if snap, _ := g.Snapshot(13); snap.State != breaker.Closed || snap.Count != 0 || snap.Active != 1 {
		t.Fatalf("setup: %+v", snap)
	}
	mustRelease(t, g, hold, false, 9999, 14) // 旧纪元：失败+慢都不入环
	snap, _ := g.Snapshot(14)
	if snap.State != breaker.Closed || snap.Count != 0 || snap.Failed != 0 || snap.Slow != 0 {
		t.Fatalf("stale epoch result must be ignored: %+v", snap)
	}
	if err := g.Release(hold, true, 1, 15); !errors.Is(err, ErrPermitNotActive) {
		t.Fatalf("double release err=%v", err)
	}
}

// TestQueueClosedOnly Open 与 HalfOpen 下队列为空、不排队。
func TestQueueClosedOnly(t *testing.T) {
	p := exampleParams()
	p.C, p.Q = 1, 2
	g, _ := New(p)
	mustAcquire(t, g, 0, Granted)
	q := mustAcquire(t, g, 1, Queued)
	if st, _ := g.Status(q.ID, 6); st != StatusTimedOut {
		t.Fatalf("q status=%v want timed out at 1+5", st)
	}
	mustRelease(t, g, 1, false, 10, 7) // 环 [失败]
	x := mustAcquire(t, g, 7, Granted)
	mustRelease(t, g, x.ID, false, 10, 8) // [失败,失败] -> Open
	if snap, _ := g.Snapshot(8); snap.State != breaker.Open || snap.Queued != 0 {
		t.Fatalf("queue must be empty in open: %+v", snap)
	}
	mustAcquire(t, g, 8, RejectedOpen)
	mustAcquire(t, g, 18, Granted) // HalfOpen
	mustAcquire(t, g, 18, RejectedHalfOpenFull)
}

// TestRejectDoesNotConsumeID 拒绝结果不耗编号。
func TestRejectDoesNotConsumeID(t *testing.T) {
	g, _ := New(exampleParams())
	mustAcquire(t, g, 0, Granted) // 1
	mustAcquire(t, g, 0, Queued)  // 2
	mustAcquire(t, g, 0, RejectedFull)
	mustAcquire(t, g, 0, RejectedFull)
	mustRelease(t, g, 1, true, 10, 1)
	mustRelease(t, g, 2, false, 10, 2) // Open
	mustAcquire(t, g, 2, RejectedOpen)
	r := mustAcquire(t, g, 12, Granted) // HalfOpen，下一个编号仍是 3
	if r.ID != 3 {
		t.Fatalf("rejections must not consume ids, got id=%d", r.ID)
	}
}

// TestErrorPrecedenceAndNoStateChange 错误优先级与“错误不改变任何状态（含 maxNow 与编号）”。
func TestErrorPrecedenceAndNoStateChange(t *testing.T) {
	g, _ := New(exampleParams())
	if err := g.Release(0, true, -1, -5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid arg first, err=%v", err)
	}
	if err := g.Release(1, true, -1, 100); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("neg dur, err=%v", err)
	}
	if _, err := g.Acquire(1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("invalid time, err=%v", err)
	}
	mustAcquire(t, g, 5, Granted) // 推进 maxNow=5，id=1
	if _, err := g.Acquire(4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback, err=%v", err)
	}
	q := mustAcquire(t, g, 6, Queued) // id=2
	if err := g.Release(q.ID, true, 1, 6); !errors.Is(err, ErrPermitNotActive) {
		t.Fatalf("release queued, err=%v", err)
	}
	if err := g.Release(999, true, 1, 6); !errors.Is(err, ErrPermitNotActive) {
		t.Fatalf("release unknown, err=%v", err)
	}
	if _, err := g.Status(999, 6); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("status unknown, err=%v", err)
	}
	mustRelease(t, g, 1, true, 10, 7)   // Closed，队首 id2 获授
	mustRelease(t, g, q.ID, true, 1, 8) // id2 归还
	if err := g.Release(1, true, 1, 9); !errors.Is(err, ErrPermitNotActive) {
		t.Fatalf("double release, err=%v", err)
	}
	// 之前对 now=4 的回退报错不得推进 maxNow；最近成功 now=8，
	// now=9 的错误（非在役）也不得推进 maxNow，故再用 now=7 应报回退。
	if _, err := g.Acquire(7); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("failed call must not advance maxNow, err=%v", err)
	}
}

// TestInvalidParams 构造参数越界全部返回 ErrInvalidArgument。
func TestInvalidParams(t *testing.T) {
	base := exampleParams()
	cases := []func(p Params) Params{
		func(p Params) Params { p.M = p.N + 1; return p },
		func(p Params) Params { p.N, p.M = 1001, 1; return p },
		func(p Params) Params { p.N, p.M = 0, 0; return p },
		func(p Params) Params { p.F = 0; return p },
		func(p Params) Params { p.F = 101; return p },
		func(p Params) Params { p.SR = 0; return p },
		func(p Params) Params { p.SR = 101; return p },
		func(p Params) Params { p.S = 0; return p },
		func(p Params) Params { p.O = 1_000_000_001; return p },
		func(p Params) Params { p.Wt = 0; return p },
		func(p Params) Params { p.H = 0; return p },
		func(p Params) Params { p.H = 101; return p },
		func(p Params) Params { p.C = 0; return p },
		func(p Params) Params { p.C = 1001; return p },
		func(p Params) Params { p.Q = -1; return p },
		func(p Params) Params { p.Q = 1001; return p },
	}
	for i, mut := range cases {
		if _, err := New(mut(base)); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: err=%v want ErrInvalidArgument", i, err)
		}
	}
}
