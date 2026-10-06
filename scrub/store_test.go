package scrub

import (
	"errors"
	"testing"
)

func mustCreate(t *testing.T, s *Store, id int, rs []Replica, interval Time) {
	t.Helper()
	if err := s.CreateBlock(0, id, rs, interval); err != nil {
		t.Fatalf("create block %d: %v", id, err)
	}
}

// TestPatrolFullFlow：位腐修复成功后立即自洽，二次巡检无需修复。
func TestPatrolFullFlow(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 1, []Replica{
		rep(10, 2, "d2"), rep(20, 2, "d2"), rep(30, 1, "d1"),
	}, 10)
	if err := s.Rot(1, 1, 20, "corrupt"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Patrol(5, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeRepaired || len(res.Repaired) != 2 {
		t.Fatalf("res=%+v", res)
	}
	got, _ := s.replicasOf(1)
	for _, r := range got {
		if r.Version != 2 || !r.Intact() {
			t.Fatalf("replica not converged: %+v", r)
		}
	}
	res2, err := s.Patrol(15, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Outcome != OutcomeNoRepair {
		t.Fatalf("second patrol outcome=%s", res2.Outcome)
	}
}

// TestPatrolPartialFailure：部分失败与“全部目标失败”均为部分修复，
// 失败副本保持原状，成功副本立即自洽。
func TestPatrolPartialFailure(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 7, []Replica{
		rep(1, 2, "d2"), rotten(rep(2, 2, "d2"), "bad"), rep(3, 1, "d1"),
	}, 100)
	res, err := s.Patrol(0, 7, map[int]bool{2: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomePartial {
		t.Fatalf("outcome=%s", res.Outcome)
	}
	if len(res.Repaired) != 1 || res.Repaired[0] != 3 {
		t.Fatalf("repaired=%v", res.Repaired)
	}
	if len(res.FailedNodes) != 1 || res.FailedNodes[0] != 2 {
		t.Fatalf("failed=%v", res.FailedNodes)
	}
	rs, _ := s.replicasOf(7)
	for _, r := range rs {
		switch r.Node {
		case 2:
			if r.ActualDigest != "bad" || r.Version != 2 {
				t.Fatalf("failed node must stay untouched: %+v", r)
			}
		case 3:
			if r.Version != 2 || !r.Intact() {
				t.Fatalf("successful node must converge: %+v", r)
			}
		}
	}

	res2, err := s.Patrol(100, 7, map[int]bool{2: true})
	if err != nil || res2.Outcome != OutcomePartial {
		t.Fatalf("all-fail: err=%v outcome=%s", err, res2.Outcome)
	}
}

// TestIntervalExactAndOneLess：恰等于间隔允许，小一秒拒绝。
func TestIntervalExactAndOneLess(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 1, []Replica{rep(1, 1, "d"), rep(2, 1, "d")}, 10)
	if _, err := s.Patrol(0, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patrol(9, 1, nil); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("t=9 want too-frequent, got %v", err)
	}
	if _, err := s.Patrol(10, 1, nil); err != nil {
		t.Fatalf("t=10 exact interval must pass: %v", err)
	}
}

// TestClockBackAndRejectionNoTrace：时钟回退优先于不存在/频繁；
// 任何拒绝都不改变时钟、不记录巡检、不留告警。
func TestClockBackAndRejectionNoTrace(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 1, []Replica{rep(1, 1, "d"), rep(2, 1, "d")}, 100)
	if _, err := s.Patrol(50, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patrol(49, 99, nil); !errors.Is(err, ErrClockBack) {
		t.Fatalf("error order wrong, got %v", err)
	}
	if _, err := s.Patrol(51, 1, nil); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("want too-frequent, got %v", err)
	}
	if err := s.Write(49, 1, 2, "d2", []int{1}); !errors.Is(err, ErrClockBack) {
		t.Fatalf("write clockback got %v", err)
	}
	if _, err := s.Due(49, 10); !errors.Is(err, ErrClockBack) {
		t.Fatalf("due clockback got %v", err)
	}
	if got := s.clockNow(); got != 50 {
		t.Fatalf("clock changed by rejected ops: %d", got)
	}
	if len(s.Alerts()) != 0 {
		t.Fatal("rejections must not create alerts")
	}
	if _, err := s.Patrol(60, 1, nil); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("rejected patrol must not record time, got %v", err)
	}
}

// TestErrorOrdering：参数非法 -> 时钟回退 -> 不存在 -> 过于频繁。
func TestErrorOrdering(t *testing.T) {
	s := NewStore()
	if err := s.CreateBlock(0, 1, []Replica{rep(1, 1, "d")}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("single replica must be invalid, got %v", err)
	}
	if err := s.Write(-1, 1, 0, "", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid must precede clockback, got %v", err)
	}
	if err := s.Rot(-5, 42, 1, "x"); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clockback must precede not-found, got %v", err)
	}
	if err := s.Discard(0, 42, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not-found, got %v", err)
	}
}

// TestDiscardQuorumDrops：丢弃后法定数与已提交版本按当前副本数推断；
// 降到一可巡检，降到零块不存在。
func TestDiscardQuorumDrops(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 5, []Replica{
		rep(1, 3, "d3"), rep(2, 3, "d3"), rep(3, 3, "d3"), rep(4, 2, "d2"),
	}, 100)
	if err := s.Discard(1, 5, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(2, 5, 2); err != nil {
		t.Fatal(err)
	}
	res, err := s.Patrol(2, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Quorum != 2 || res.CommittedVersion != 2 {
		t.Fatalf("after discard quorum=%d committed=%d want 2,2", res.Quorum, res.CommittedVersion)
	}
	if res.AuthorityVersion != 3 {
		t.Fatalf("intact v3 must still serve as authority, got %d", res.AuthorityVersion)
	}

	if err := s.Discard(3, 5, 4); err != nil {
		t.Fatal(err)
	}
	res2, err := s.Patrol(103, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Quorum != 1 || res2.Outcome != OutcomeNoRepair {
		t.Fatalf("single replica patrol: %+v", res2)
	}

	if err := s.Discard(104, 5, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patrol(105, 5, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not-found after full discard, got %v", err)
	}
	if _, ok := s.replicasOf(5); ok {
		t.Fatal("block must be gone")
	}
}

// TestWriteReCreatesReplica：节点副本丢弃后可被新写入重新创建。
func TestWriteReCreatesReplica(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 1, []Replica{rep(1, 1, "d1"), rep(2, 1, "d1")}, 100)
	if err := s.Discard(1, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(2, 1, 3, "d3", []int{2}); err != nil {
		t.Fatal(err)
	}
	rs, _ := s.replicasOf(1)
	if len(rs) != 2 {
		t.Fatalf("replica should be re-created, got %d", len(rs))
	}
}

// TestAlertsAppendOnly：同一块重复告警不合并。
func TestAlertsAppendOnly(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 1, []Replica{
		rotten(rep(1, 1, "d"), "x"), rotten(rep(2, 1, "d"), "y"),
	}, 100)
	mustCreate(t, s, 2, []Replica{
		rep(1, 3, "a"), rep(2, 3, "b"),
	}, 100)
	for i := 0; i < 2; i++ {
		if _, err := s.Patrol(Time(i*100), 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Patrol(100, 2, nil); err != nil {
		t.Fatal(err)
	}
	alerts := s.Alerts()
	if len(alerts) != 3 {
		t.Fatalf("want 3 unmerged alerts, got %d: %+v", len(alerts), alerts)
	}
	for i, a := range alerts {
		if a.Seq != i+1 {
			t.Fatalf("alert sequence broken: %+v", a)
		}
	}
	if alerts[0].Outcome != OutcomeNoSource || alerts[2].Outcome != OutcomeConflict {
		t.Fatalf("unexpected alert types: %+v", alerts)
	}
}

// TestDueSelectionOrder：从未巡检优先；已巡检按上次巡检时刻升序，
// 并列块号小者；limit 截断；选取本身只读。
func TestDueSelectionOrder(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, 30, []Replica{rep(1, 1, "d"), rep(2, 1, "d")}, 100)
	mustCreate(t, s, 10, []Replica{rep(1, 1, "d"), rep(2, 1, "d")}, 100)
	mustCreate(t, s, 20, []Replica{rep(1, 1, "d"), rep(2, 1, "d")}, 100)
	if _, err := s.Patrol(0, 20, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.Due(50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 10 || got[1] != 30 {
		t.Fatalf("never-scrubbed order: %v", got)
	}

	if _, err := s.Patrol(50, 10, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patrol(50, 30, nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.Due(150, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{20, 10, 30}
	if len(got) != 3 {
		t.Fatalf("due list: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("due order=%v want %v", got, want)
		}
	}

	got, _ = s.Due(150, 2)
	if len(got) != 2 || got[0] != 20 || got[1] != 10 {
		t.Fatalf("limit truncation: %v", got)
	}

	got2, _ := s.Due(150, 10)
	for i := range want {
		if got2[i] != want[i] {
			t.Fatalf("Due must be read-only: %v", got2)
		}
	}
}
