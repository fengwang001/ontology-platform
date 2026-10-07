package ontology

import (
	"testing"
	"time"
)

func TestAcquireNonBlockingReject(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	if _, err := s.Acquire("A1", "T1"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// 第二个动作立即收到明确的"已被占用"拒绝。
	_, err := s.Acquire("A2", "T1")
	if code := rejectCode(t, err); code != RejectOccupiedByAction {
		t.Fatalf("code = %s, want %s", code, RejectOccupiedByAction)
	}
}

func TestStagedChangesInvisibleUntilCommit(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a", "state": "open"})

	occ, _ := s.Acquire("A1", "T1")
	if err := occ.Apply(
		Mutation{Property: "state", Value: "closing"},
		Mutation{Property: "title", Value: "half"},
	); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// 占用期间的读取只能看到占用开始之前的完整状态。
	snap, _ := s.Get("T1")
	if snap.Version != 1 || snap.Properties["state"] != "open" || snap.Properties["title"] != "a" {
		t.Fatalf("read observed intermediate state: %+v", snap)
	}
	if !snap.Occupied || snap.Holder != "A1" {
		t.Fatalf("occupancy not observable: %+v", snap)
	}
	if _, err := occ.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// 提交后读取看到占用结束之后的完整状态。
	snap, _ = s.Get("T1")
	if snap.Version != 2 || snap.Properties["state"] != "closing" || snap.Properties["title"] != "half" {
		t.Fatalf("post-commit state: %+v", snap)
	}
	if snap.Occupied {
		t.Fatalf("occupancy should be released: %+v", snap)
	}
}

func TestAbortDiscardsStagedChanges(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a", "state": "open"})

	occ, _ := s.Acquire("A1", "T1")
	occ.Apply(Mutation{Property: "state", Value: "closed"})
	if err := occ.Abort(); err != nil {
		t.Fatalf("abort: %v", err)
	}
	snap, _ := s.Get("T1")
	if snap.Version != 1 || snap.Properties["state"] != "open" {
		t.Fatalf("abort leaked staged state: %+v", snap)
	}
	// 释放后普通更新恢复按乐观规则判定。
	if _, err := s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"}); err != nil {
		t.Fatalf("update after abort: %v", err)
	}
}

func TestLeaseExpiryAllowsTakeover(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	s := newTestStore(t, WithClock(clock), WithLeaseTTL(10*time.Second))
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	occ1, _ := s.Acquire("A1", "T1")
	tok1 := occ1.Token()

	// 租约未到期：不得提前释放给其他申请方。
	clock.Advance(9 * time.Second)
	if _, err := s.Acquire("A2", "T1"); rejectCode(t, err) != RejectOccupiedByAction {
		t.Fatal("premature takeover before lease expiry")
	}

	// 持有方异常中止（无心跳、无显式释放），租约到期是判定失效的唯一依据。
	clock.Advance(1 * time.Second)
	occ2, err := s.Acquire("A2", "T1")
	if err != nil {
		t.Fatalf("takeover after expiry: %v", err)
	}
	if occ2.Token() <= tok1 {
		t.Fatalf("fencing token not increasing: %d -> %d", tok1, occ2.Token())
	}

	// 旧持有方的任何操作都必须因栅栏令牌不匹配而失败。
	if err := occ1.Apply(Mutation{Property: "title", Value: "zombie"}); rejectCode(t, err) != RejectOccupancyLost {
		t.Fatalf("stale apply should lose occupancy, got %v", err)
	}
	if _, err := occ1.Commit(); rejectCode(t, err) != RejectOccupancyLost {
		t.Fatalf("stale commit should lose occupancy, got %v", err)
	}
	snap, _ := s.Get("T1")
	if snap.Version != 1 || snap.Properties["title"] != "a" {
		t.Fatalf("stale holder modified state: %+v", snap)
	}
}

func TestHeartbeatExtendsLease(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	s := newTestStore(t, WithClock(clock), WithLeaseTTL(10*time.Second))
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	occ, _ := s.Acquire("A1", "T1")
	for i := 0; i < 5; i++ {
		clock.Advance(9 * time.Second)
		if err := occ.Heartbeat(); err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
		if _, err := s.Acquire("A2", "T1"); rejectCode(t, err) != RejectOccupiedByAction {
			t.Fatal("heartbeat failed to keep lease alive")
		}
	}
	// 停止心跳后租约到期，占用可被重新获得。
	clock.Advance(10 * time.Second)
	if _, err := s.Acquire("A2", "T1"); err != nil {
		t.Fatalf("acquire after heartbeat stopped: %v", err)
	}
	// 旧持有方心跳也应失败。
	if err := occ.Heartbeat(); rejectCode(t, err) != RejectOccupancyLost {
		t.Fatalf("stale heartbeat should lose occupancy, got %v", err)
	}
}

func TestCommitAfterReleaseRejected(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	occ, _ := s.Acquire("A1", "T1")
	if _, err := occ.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := occ.Commit(); rejectCode(t, err) != RejectOccupancyLost {
		t.Fatalf("double commit should fail, got %v", err)
	}
}
