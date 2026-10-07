package ontology

import "testing"

// 持有方异常中止且未显式释放：租约未到期前占用绝不提前转交；
// 唯一裁定依据（系统时钟超过 expiresAt）满足后，后续申请方可获得占用，
// 且旧 Lease 上的任何操作都因围栏/租约校验失败而被拒绝。
func TestLeaseExpiryAndFencing(t *testing.T) {
	s, clk := newTestStore()
	s.CreateInstance("o1", "open", nil)

	l1, got := s.TryAcquire("act-A", []string{"o1"}, 100)
	if got != OutcomeCommitted {
		t.Fatal(got)
	}

	// 持有方“中止”后、租约尚未到期：申请方仍被拒绝，占用不提前释放。
	clk.Advance(50)
	if _, got := s.TryAcquire("act-B", []string{"o1"}, 100); got != OutcomeOccupied {
		t.Fatalf("pre-expiry acquire = %v, want Occupied", got)
	}
	if snap, _ := s.Snapshot("o1"); !snap.Occupied || snap.Holder != "act-A" {
		t.Fatalf("lease released early: %+v", snap)
	}

	// 到达唯一裁定时刻：旧 Lease 操作被拒（InvalidLease），占用可被重新获得。
	clk.Advance(51)
	if got := l1.Stage("o1", "done", nil, nil); got != OutcomeInvalidLease {
		t.Fatalf("stale lease stage = %v, want InvalidLease", got)
	}
	if got := l1.Commit(); got != OutcomeInvalidLease {
		t.Fatalf("stale lease commit = %v, want InvalidLease", got)
	}

	l2, got := s.TryAcquire("act-B", []string{"o1"}, 100)
	if got != OutcomeCommitted {
		t.Fatalf("post-expiry acquire = %v, want committed", got)
	}
	// “中止”的旧持有方晚到的释放绝不能释放新持有方的占用（围栏效应）。
	if got := l1.Release(); got != OutcomeInvalidLease {
		t.Fatalf("zombie release = %v, want InvalidLease", got)
	}
	if snap, _ := s.Snapshot("o1"); snap.Holder != "act-B" {
		t.Fatalf("zombie release stole lease: %+v", snap)
	}

	// 到期事件必须作为可观察事件记录，含唯一裁定依据。
	var sawExpiry bool
	for _, d := range s.Log().Entries() {
		if d.Kind == "lease_expired" {
			sawExpiry = true
			if d.At != 101 {
				t.Fatalf("expiry adjudicated at %d, want 101", d.At)
			}
		}
	}
	if !sawExpiry {
		t.Fatal("no lease_expired decision recorded")
	}
	l2.Release()
}

// 心跳在租约存活时续期；超过原 TTL 但心跳及时的占用仍然有效。
func TestHeartbeat(t *testing.T) {
	s, clk := newTestStore()
	s.CreateInstance("o1", "open", nil)
	l, _ := s.TryAcquire("act", []string{"o1"}, 100)

	for i := 0; i < 5; i++ {
		clk.Advance(80)
		if got := l.Heartbeat(); got != OutcomeCommitted {
			t.Fatalf("heartbeat %d = %v", i, got)
		}
	}
	clk.Advance(50)
	if _, got := s.TryAcquire("other", []string{"o1"}, 100); got != OutcomeOccupied {
		t.Fatalf("heartbeat-kept lease acquire = %v, want Occupied", got)
	}

	clk.Advance(200)
	if got := l.Heartbeat(); got != OutcomeInvalidLease {
		t.Fatalf("heartbeat after expiry = %v, want InvalidLease", got)
	}
}

// Release 与 Commit 都是一次性事件，重复调用必须被拒绝。
func TestDoubleTerminal(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "open", Props{"a": "0"})
	l, _ := s.TryAcquire("act", []string{"o1"}, 10_000)
	l.Stage("o1", "done", Patch{"a": "1"}, nil)
	if got := l.Commit(); got != OutcomeCommitted {
		t.Fatal(got)
	}
	if got := l.Commit(); got != OutcomeInvalidLease {
		t.Fatalf("double commit = %v", got)
	}
	if got := l.Release(); got != OutcomeInvalidLease {
		t.Fatalf("release after commit = %v", got)
	}
	snap, _ := s.Snapshot("o1")
	if snap.State != "done" || snap.Props["a"] != "1" || snap.Version != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}

	l2, _ := s.TryAcquire("act2", []string{"o1"}, 10_000)
	if got := l2.Release(); got != OutcomeCommitted {
		t.Fatal(got)
	}
	if snap, _ := s.Snapshot("o1"); snap.Version != 2 || snap.State != "done" {
		t.Fatalf("release altered committed state: %+v", snap)
	}
}
