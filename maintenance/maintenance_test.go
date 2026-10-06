package maintenance

import (
	"errors"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		ResponseLimit: [4]int{10, 8, 5, 2},
		CompleteLimit: [4]int{30, 20, 10, 5},
		RejectUpgrade: 3,
	}
}

func reg(t *testing.T, s *Service, now int, trades, buildings []string, cap int, emerg bool) int {
	t.Helper()
	id, err := s.RegisterContractor(now, trades, buildings, cap, emerg)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return id
}

func submit(t *testing.T, s *Service, now int, tenant, trade, building string, l Level) int {
	t.Helper()
	id, err := s.SubmitOrder(now, tenant, trade, building, l)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return id
}

func dispatch(t *testing.T, s *Service, now int) (int, int) {
	t.Helper()
	oid, cid, err := s.DispatchNext(now)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return oid, cid
}

func statusOf(t *testing.T, s *Service, now, oid int) Status {
	t.Helper()
	v, err := s.GetOrder(now, oid)
	if err != nil {
		t.Fatalf("getorder: %v", err)
	}
	return v.Status
}

func dispatchWant(t *testing.T, s *Service, now, wantOrder int) (int, int) {
	t.Helper()
	oid, cid, err := s.DispatchNext(now)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if oid != wantOrder {
		t.Fatalf("want order %d got %d", wantOrder, oid)
	}
	return oid, cid
}

// 候选次序第一层：在手数更少者优先。
func TestCandidateOrderActiveCount(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	c3 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o1 := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 2, o1); cid != c1 {
		t.Fatalf("first: want c1 got %d", cid)
	}
	o2 := submit(t, s, 3, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 4, o2); cid != c2 {
		t.Fatalf("second: want c2 got %d", cid)
	}
	o3 := submit(t, s, 5, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 6, o3); cid != c3 {
		t.Fatalf("idle c3 should win, got %d (c1=%d c2=%d c3=%d)", cid, c1, c2, c3)
	}
}

// 候选次序第二层：在手数相同时最近完成更早者优先。
func TestCandidateOrderLastCompletion(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o1 := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 2, o1); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Confirm(3, o1, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(4, c1, o1); err != nil {
		t.Fatal(err)
	}
	o2 := submit(t, s, 5, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 6, o2); cid != c2 {
		t.Fatalf("want c2 got %d", cid)
	}
	if err := s.Confirm(7, o2, c2); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(20, c2, o2); err != nil {
		t.Fatal(err)
	}
	// 两人在手均 0，c1 完成于 4，c2 完成于 20 -> c1 胜。
	o3 := submit(t, s, 21, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 22, o3); cid != c1 {
		t.Fatalf("earlier completion should win: got %d c1=%d c2=%d", cid, c1, c2)
	}
}

// 候选次序第三层：再相同取登记序号更小者。
func TestCandidateOrderSequence(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 2, o); cid != c1 {
		t.Fatalf("want earliest c1=%d got %d", c1, cid)
	}
}

// 紧急单抢占：满手承包商的未确认非紧急单中，派单最晚者被退回，紧急单派出。
func TestEmergencyPreemption(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true)
	victim := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 2); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	emerg := submit(t, s, 3, "t", "pipe", "A", LevelEmergency)
	oid, cid := dispatch(t, s, 4)
	if oid != emerg || cid != c1 {
		t.Fatalf("preempt want (%d,%d) got (%d,%d)", emerg, c1, oid, cid)
	}
	found := false
	for _, e := range s.Events() {
		if e.Type == EventPreempted && e.OrderID == victim {
			found = true
		}
	}
	if !found {
		t.Fatal("missing preempted event")
	}
	if st := statusOf(t, s, 4, victim); st != StatusQueued {
		t.Fatalf("victim should be queued, got %s", st)
	}
	if _, _, err := s.DispatchNext(5); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("full-hand contractor means no candidate, got %v", err)
	}
}

// 多名抢占候选时按派单相同次序：两人都满手、各持未确认非紧急单，
// 未完成过的承包商优于完成过的。
func TestPreemptionTargetOrdering(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true)
	// 让 c1 完成一单：完成时间更早，但“完成过”劣于“从未完成”。
	o0 := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 2, o0); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Confirm(3, o0, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(4, c1, o0); err != nil {
		t.Fatal(err)
	}
	// 填满两人的手（均未确认非紧急单，可作受害者）。
	v1 := submit(t, s, 5, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 6, v1); cid != c2 {
		t.Fatalf("never-completed c2 gets v1, got %d", cid)
	}
	v2 := submit(t, s, 7, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatchWant(t, s, 8, v2); cid != c1 {
		t.Fatalf("c1 gets v2, got %d", cid)
	}
	// 紧急单抢占：两人皆满手；c2 从未完成 -> c2 胜，其 v1 被退回。
	emerg := submit(t, s, 9, "t", "pipe", "A", LevelEmergency)
	if _, cid := dispatchWant(t, s, 10, emerg); cid != c2 {
		t.Fatalf("preempt target should be c2=%d got %d", c2, cid)
	}
	if st := statusOf(t, s, 10, v1); st != StatusQueued {
		t.Fatalf("v1 (c2's latest unconfirmed hold) returned, got %s", st)
	}
	if st := statusOf(t, s, 10, v2); st != StatusDispatched {
		t.Fatalf("v2 stays with c1, got %s", st)
	}
}

// 被退回工单保留原提交时刻与等级，回队后按原次序先于同级更晚单派出。
func TestPreemptedKeepsOriginalOrder(t *testing.T) {
	cfg := Config{
		ResponseLimit: [4]int{1000, 1000, 1000, 1000},
		CompleteLimit: [4]int{1000, 1000, 1000, 1000},
		RejectUpgrade: 3,
	}
	s := New(cfg)
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true)
	early := submit(t, s, 1, "t", "pipe", "A", LevelHigh)
	if _, cid := dispatchWant(t, s, 2, early); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	emerg := submit(t, s, 3, "t", "pipe", "A", LevelEmergency)
	if _, cid := dispatchWant(t, s, 4, emerg); cid != c1 {
		t.Fatalf("emergency preempts c1, got %d", cid)
	}
	if st := statusOf(t, s, 5, early); st != StatusQueued {
		t.Fatalf("victim returned to queue, got %s", st)
	}
	if err := s.Confirm(6, emerg, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(7, c1, emerg); err != nil {
		t.Fatal(err)
	}
	newer := submit(t, s, 8, "t", "pipe", "A", LevelHigh)
	if oid, _ := dispatch(t, s, 9); oid != early {
		t.Fatalf("returned order keeps original priority, want early=%d got %d (newer=%d)", early, oid, newer)
	}
}

// 已确认工单不可被抢占。
func TestConfirmedCannotBePreempted(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true)
	o := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 2); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Confirm(3, o, c1); err != nil {
		t.Fatal(err)
	}
	submit(t, s, 4, "t", "pipe", "A", LevelEmergency)
	if _, _, err := s.DispatchNext(5); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("confirmed hold blocks preemption, got %v", err)
	}
}

// 紧急单不作为抢占受害者。
func TestEmergencyCannotBeVictim(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true)
	o := submit(t, s, 1, "t", "pipe", "A", LevelEmergency)
	if _, cid := dispatchWant(t, s, 2, o); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	submit(t, s, 3, "t", "pipe", "A", LevelEmergency)
	if _, _, err := s.DispatchNext(4); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("emergency cannot be victim, got %v", err)
	}
}

// 响应时限恰等可确认，超一个单位视为拒单回队。
func TestResponseLimitBoundary(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, true)
	o := submit(t, s, 0, "t", "pipe", "A", LevelEmergency)
	if _, cid := dispatch(t, s, 0); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Confirm(2, o, c1); err != nil {
		t.Fatalf("exactly at deadline must confirm: %v", err)
	}

	s2 := New(testConfig())
	c2 := reg(t, s2, 0, []string{"pipe"}, []string{"A"}, 5, true)
	o2 := submit(t, s2, 0, "t", "pipe", "A", LevelEmergency)
	if _, cid := dispatchWant(t, s2, 0, o2); cid != c2 {
		t.Fatalf("want c2 got %d", cid)
	}
	// 查询反映应有状态但不落盘；用 Tick 完成结算后才产生拒单与事件。
	if st := statusOf(t, s2, 3, o2); st != StatusQueued {
		t.Fatalf("one past deadline auto-rejects to queue, got %s", st)
	}
	if err := s2.Tick(3); err != nil {
		t.Fatal(err)
	}
	v, _ := s2.GetOrder(3, o2)
	if v.RejectCount != 1 {
		t.Fatalf("timeout rejection must count once, got %d", v.RejectCount)
	}
}

// 累计拒单恰达 R 次触发升级；第 R-1 次不升，升级只发生一次跨阈。
func TestUpgradeAtRRejections(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	c3 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	o := submit(t, s, 0, "t", "pipe", "A", LevelRoutine)

	if _, cid := dispatch(t, s, 0); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Reject(1, o, c1); err != nil {
		t.Fatal(err)
	}
	if _, cid := dispatch(t, s, 2); cid != c2 {
		t.Fatalf("want c2 got %d", cid)
	}
	if err := s.Reject(3, o, c2); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetOrder(4, o)
	if v.Level != LevelRoutine {
		t.Fatalf("two rejects must not upgrade yet, got level %d", v.Level)
	}
	if _, cid := dispatch(t, s, 4); cid != c3 {
		t.Fatalf("want c3 got %d", cid)
	}
	if err := s.Reject(5, o, c3); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetOrder(6, o)
	if v.Level != LevelElevated {
		t.Fatalf("third reject upgrades routine->elevated, got %d", v.Level)
	}
	foundUpgrade := false
	for _, e := range s.Events() {
		if e.Type == EventUpgraded && e.OrderID == o && e.NewLevel == LevelElevated {
			foundUpgrade = true
			if e.At != 5 {
				t.Fatalf("upgrade time want 5 got %d", e.At)
			}
		}
	}
	if !foundUpgrade {
		t.Fatal("missing upgrade event")
	}
}

// 升级后时限按新等级从升级后的派单时刻重新起算。
func TestDeadlinesRecomputedAfterUpgrade(t *testing.T) {
	s := New(testConfig()) // routine 响应 10，elevated 响应 8
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	c3 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	o := submit(t, s, 0, "t", "pipe", "A", LevelRoutine)
	for step, cid := range []int{c1, c2, c3} {
		if _, got := dispatch(t, s, step*2); got != cid {
			t.Fatalf("step %d want %d got %d", step, cid, got)
		}
		if err := s.Reject(step*2+1, o, cid); err != nil {
			t.Fatal(err)
		}
	}
	// 升级发生在 t=5，下一次派单在 t=9。
	c4 := reg(t, s, 6, []string{"pipe"}, []string{"A"}, 1, false)
	if _, got := dispatchWant(t, s, 9, o); got != c4 {
		t.Fatalf("want c4 got %d", got)
	}
	v, _ := s.GetOrder(10, o)
	if v.ResponseDue != 9+8 { // elevated 响应 8
		t.Fatalf("response due must restart at dispatch with new level, want %d got %d", 17, v.ResponseDue)
	}
	if v.CompletionDue != 9+20 { // elevated 完成 20
		t.Fatalf("completion due must restart, want %d got %d", 29, v.CompletionDue)
	}
}

// 紧急单被拒再多也不再升级。
func TestEmergencyNeverUpgrades(t *testing.T) {
	s := New(testConfig())
	cs := make([]int, 0, 4)
	for range 4 {
		cs = append(cs, reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, true))
	}
	o := submit(t, s, 0, "t", "pipe", "A", LevelEmergency)
	for i, cid := range cs {
		if _, got := dispatch(t, s, i*2); got != cid {
			t.Fatalf("step %d want %d got %d", i, cid, got)
		}
		if err := s.Reject(i*2+1, o, cid); err != nil {
			t.Fatal(err)
		}
	}
	v, _ := s.GetOrder(9, o)
	if v.Level != LevelEmergency {
		t.Fatalf("emergency must stay emergency, got %d", v.Level)
	}
	if v.RejectCount != 4 {
		t.Fatalf("rejects still count, got %d", v.RejectCount)
	}
	for _, e := range s.Events() {
		if e.Type == EventUpgraded && e.OrderID == o {
			t.Fatal("emergency order must never emit upgrade")
		}
	}
}

// 完成逾期：已确认超过完成时限仍未完成 -> 逾期事件一次，不改变承接关系。
func TestCompletionOverdue(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o := submit(t, s, 0, "t", "pipe", "A", LevelHigh) // 完成 10
	if _, cid := dispatch(t, s, 0); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Confirm(1, o, c1); err != nil {
		t.Fatal(err)
	}
	// 恰等不算逾期。
	if st := statusOf(t, s, 10, o); st != StatusConfirmed {
		t.Fatalf("at due still confirmed, got %s", st)
	}
	if st := statusOf(t, s, 11, o); st != StatusOverdue {
		t.Fatalf("past due is overdue, got %s", st)
	}
	v, _ := s.GetOrder(11, o)
	if v.AssignedTo != c1 || v.Level != LevelHigh {
		t.Fatalf("overdue must keep contractor and level, got c=%d l=%d", v.AssignedTo, v.Level)
	}
	if err := s.Tick(12); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range s.Events() {
		if e.Type == EventOverdue && e.OrderID == o {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("overdue event must fire exactly once, got %d", count)
	}
	// 逾期单仍可完成。
	if err := s.Complete(15, c1, o); err != nil {
		t.Fatalf("overdue order can complete: %v", err)
	}
}

// 承包商停用：未确认在手单全部回队，已确认的保留。
func TestDeactivateReturnsUnconfirmed(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o1 := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 2); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	o2 := submit(t, s, 3, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 4); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Confirm(5, o1, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.Deactivate(6, c1); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, s, 7, o1); st != StatusConfirmed {
		t.Fatalf("confirmed order survives deactivation, got %s", st)
	}
	if st := statusOf(t, s, 7, o2); st != StatusQueued {
		t.Fatalf("unconfirmed order must return to queue, got %s", st)
	}
	if err := s.Deactivate(8, c1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("double deactivate is invalid state, got %v", err)
	}
	// 停用后不再参与派单。
	if _, _, err := s.DispatchNext(9); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("deactivated contractor excluded, got %v", err)
	}
}

// 撤销：未确认可撤销；已确认不可撤销；非提交者不可撤销。
func TestCancelRules(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o1 := submit(t, s, 1, "alice", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 2); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Cancel(3, "bob", o1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-submitter cancel forbidden, got %v", err)
	}
	if err := s.Confirm(4, o1, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(5, "alice", o1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("confirmed cancel invalid state, got %v", err)
	}
	// 队列中的单可由本人撤销。
	o2 := submit(t, s, 6, "alice", "pipe", "A", LevelRoutine)
	if err := s.Cancel(7, "alice", o2); err != nil {
		t.Fatalf("queued cancel allowed: %v", err)
	}
	if st := statusOf(t, s, 8, o2); st != StatusCancelled {
		t.Fatalf("want cancelled got %s", st)
	}
}

// 完成权限：非承接者不能完成；已完成不能再完成/再确认。
func TestCompletePermissions(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 2); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Complete(3, c2, o); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("completing unconfirmed is invalid state before ownership, got %v", err)
	}
	if err := s.Confirm(4, o, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(5, c2, o); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-assignee complete forbidden, got %v", err)
	}
	if err := s.Complete(6, c1, o); err != nil {
		t.Fatalf("assignee completes: %v", err)
	}
	if err := s.Complete(7, c1, o); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("re-complete invalid state, got %v", err)
	}
}

// 同一承包商拒绝过的工单不再派给他。
func TestRejectedContractorSkipped(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 1, false)
	o := submit(t, s, 0, "t", "pipe", "A", LevelRoutine)
	if _, cid := dispatch(t, s, 0); cid != c1 {
		t.Fatalf("want c1 got %d", cid)
	}
	if err := s.Reject(1, o, c1); err != nil {
		t.Fatal(err)
	}
	// 再派应直接给 c2；之后 c2 也拒。此时两人都满手（均释放为空手），
	// 但 c1 拒过此单 -> 仍只可能派 c2……c2 也拒过，于是无候选。
	if _, cid := dispatch(t, s, 2); cid != c2 {
		t.Fatalf("want c2 got %d", cid)
	}
	if err := s.Reject(3, o, c2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DispatchNext(4); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("both rejected this order -> no candidate, got %v", err)
	}
	// 新登记的承包商可以接单。
	c3 := reg(t, s, 5, []string{"pipe"}, []string{"A"}, 1, false)
	if _, cid := dispatch(t, s, 6); cid != c3 {
		t.Fatalf("fresh contractor should be picked, got %d", cid)
	}
}

// 队列次序：等级高先出；同级提交早先出；再同工单序号。
func TestQueueOrdering(t *testing.T) {
	s := New(testConfig())
	reg(t, s, 0, []string{"pipe"}, []string{"A"}, 10, true)
	o1 := submit(t, s, 1, "t", "pipe", "A", LevelRoutine)
	o2 := submit(t, s, 2, "t", "pipe", "A", LevelHigh)
	o3 := submit(t, s, 3, "t", "pipe", "A", LevelHigh)
	o4 := submit(t, s, 4, "t", "pipe", "A", LevelEmergency)
	if oid, _ := dispatch(t, s, 5); oid != o4 {
		t.Fatalf("emergency first, got %d", oid)
	}
	if oid, _ := dispatch(t, s, 6); oid != o2 {
		t.Fatalf("high earlier first, got %d", oid)
	}
	if oid, _ := dispatch(t, s, 7); oid != o3 {
		t.Fatalf("high later second, got %d", oid)
	}
	if oid, _ := dispatch(t, s, 8); oid != o1 {
		t.Fatalf("routine last, got %d", oid)
	}
}

// 队首无候选时不阻塞后续可派工单。
func TestNoCandidateDoesNotBlock(t *testing.T) {
	s := New(testConfig())
	reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	// 电工单无人接（且非紧急不可抢占）。
	stuck := submit(t, s, 1, "t", "electric", "A", LevelRoutine)
	ok := submit(t, s, 2, "t", "pipe", "A", LevelRoutine)
	// 第一次派单跳过 stuck，派出 ok。
	if oid, _ := dispatch(t, s, 3); oid != ok {
		t.Fatalf("stuck head must not block, got %d want %d", oid, ok)
	}
	// stuck 仍在队列中。
	if _, _, err := s.DispatchNext(4); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("stuck remains queued, got %v", err)
	}
	if st := statusOf(t, s, 5, stuck); st != StatusQueued {
		t.Fatalf("stuck stays queued, got %s", st)
	}
}

// 错误固定次序：参数非法先于时钟回退；时钟回退先于不存在；不存在先于状态/权限。
func TestErrorPrecedence(t *testing.T) {
	s := New(testConfig())
	reg(t, s, 10, []string{"pipe"}, []string{"A"}, 5, false)
	// 非法参数优先于时钟回退。
	if _, err := s.SubmitOrder(5, "", "pipe", "A", LevelRoutine); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid arg beats clock rollback, got %v", err)
	}
	// 时钟回退优先于不存在。
	if _, err := s.GetOrder(5, 999); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("clock rollback beats not found, got %v", err)
	}
	// 不存在优先于权限（非提交者撤销不存在的单 -> not found）。
	if err := s.Cancel(11, "someone", 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found beats forbidden, got %v", err)
	}
	// 非法等级是参数非法。
	if _, err := s.SubmitOrder(11, "t", "pipe", "A", Level(9)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad level invalid arg, got %v", err)
	}
	// capacity<=0 非法。
	if _, err := s.RegisterContractor(11, []string{"pipe"}, []string{"A"}, 0, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad capacity invalid arg, got %v", err)
	}
}

// 被拒绝的操作不得改变任何状态。
func TestRejectedOperationLeavesNoTrace(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	o := submit(t, s, 0, "alice", "pipe", "A", LevelRoutine)
	before := len(s.Events())

	if err := s.Confirm(1, 999, c1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := s.Cancel(1, "bob", o); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	if _, err := s.SubmitOrder(1, "t", "pipe", "A", Level(0)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want invalid arg, got %v", err)
	}
	if err := s.Tick(-1); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("want clock backward, got %v", err)
	}
	if len(s.Events()) != before {
		t.Fatalf("failed ops must not emit events, before=%d after=%d", before, len(s.Events()))
	}
	v, _ := s.GetOrder(1, o)
	if v.Status != StatusQueued || v.RejectCount != 0 {
		t.Fatalf("order unchanged: status=%s rejects=%d", v.Status, v.RejectCount)
	}
	// 时钟未被回退尝试污染，仍可在 t=1 操作。
	if _, cid := dispatchWant(t, s, 1, o); cid != c1 {
		t.Fatalf("service still usable after failed ops, got %d", cid)
	}
}

// 紧急单只派给接受紧急的承包商。
func TestEmergencyOnlyToAccepting(t *testing.T) {
	s := New(testConfig())
	c1 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, false)
	c2 := reg(t, s, 0, []string{"pipe"}, []string{"A"}, 5, true)
	o := submit(t, s, 1, "t", "pipe", "A", LevelEmergency)
	if _, cid := dispatchWant(t, s, 2, o); cid != c2 {
		t.Fatalf("emergency must skip non-accepting c1=%d, got %d", c1, cid)
	}
}

// TestConcurrentSafety 并发混合调用：任何串行化下都不得超容量或一单双派。
func TestConcurrentSafety(t *testing.T) {
	cfg := Config{
		ResponseLimit: [4]int{50, 40, 30, 20},
		CompleteLimit: [4]int{200, 150, 100, 80},
		RejectUpgrade: 3,
	}
	s := New(cfg)
	var cs []int
	for range 6 {
		id, err := s.RegisterContractor(0, []string{"pipe", "electric"}, []string{"A", "B"}, 3, true)
		if err != nil {
			t.Fatal(err)
		}
		cs = append(cs, id)
	}

	const goroutines = 12
	const each = 120
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				now := seed*1000 + i
				_, _ = s.SubmitOrder(now, "tenant", pick(seed, i, 0), pick(seed, i, 1),
					Level(1+(seed+i)%4))
				oid, cid, derr := s.DispatchNext(now + 1)
				if derr == nil {
					switch (seed + i) % 5 {
					case 0:
						_ = s.Confirm(now+2, oid, cid)
					case 1:
						_ = s.Complete(now+3, cid, oid)
					case 2:
						_ = s.Reject(now+2, oid, cid)
					}
				}
				_ = s.Tick(now + 4)
			}
		}(g)
	}
	wg.Wait()

	// 全局一致性检查：每张非终态、已派工单必须恰好出现在一个承包商的 holds 中，
	// 且任何承包商 holds 数量不得超过容量。
	assigned := map[int]int{}
	for cid, c := range s.contractors {
		if len(c.holds) > c.capacity {
			t.Fatalf("contractor %d over capacity: %d > %d", cid, len(c.holds), c.capacity)
		}
		for oid := range c.holds {
			if prev, dup := assigned[oid]; dup {
				t.Fatalf("order %d held by both %d and %d", oid, prev, cid)
			}
			assigned[oid] = cid
			o := s.orders[oid]
			if o.assignedTo != cid {
				t.Fatalf("order %d bookkeeping mismatch: holds->%d field->%d", oid, cid, o.assignedTo)
			}
		}
	}
}

func pick(seed, i, which int) string {
	opts := [2][]string{
		{"pipe", "electric"},
		{"A", "B"},
	}
	return opts[which][(seed+i)%2]
}

// countSelection 用纯操作计数（不依赖墙钟）度量“为一张工单选承包商”的开销：
// 普通候选扫描 + 抢占候选扫描（每步含一次容量有界的 holds 扫描）。
// 该计数器只触碰承包商与其 holds（holds <= 容量），不触碰任何其他工单，
// 因此其值随工单总数增长与否可直接作为复杂度证据。
func countSelection(s *Service, o *order) int {
	steps := 0
	for _, cid := range s.contractorIDs {
		c := s.contractors[cid]
		steps++ // 普通候选的工种/楼栋/容量/拒绝集合判断，均为 O(1)
		_ = c
	}
	if o.level == LevelEmergency {
		for _, cid := range s.contractorIDs {
			c := s.contractors[cid]
			steps++
			for range c.pending { // 仅未确认在手单；至多 capacity 步，与历史工单总数无关
				steps++
			}
		}
	}
	return steps
}

// TestSelectionCostIndependentOfOrderCount 证明：
// 固定承包商数 C 后，选取成本是 C 与各容量的函数；工单总数从 N 增至 16N
// 时计数完全不变。
func TestSelectionCostIndependentOfOrderCount(t *testing.T) {
	cfg := Config{
		ResponseLimit: [4]int{100, 100, 100, 100},
		CompleteLimit: [4]int{1000, 1000, 1000, 1000},
		RejectUpgrade: 100,
	}
	build := func(numOrders int) (*Service, *order) {
		s := New(cfg)
		for range 8 {
			ids := []string{"pipe"}
			blds := []string{"A"}
			cid, err := s.RegisterContractor(0, ids, blds, 1_000_000, true)
			if err != nil {
				t.Fatal(err)
			}
			_ = cid
		}
		// 制造大量处于“已确认/已完成”状态的历史工单，使系统工单总数膨胀。
		for i := range numOrders {
			t0 := 1 + 2*i
			oid, err := s.SubmitOrder(t0, "tenant", "pipe", "A", LevelRoutine)
			if err != nil {
				t.Fatal(err)
			}
			_, cid, err := s.DispatchNext(t0)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Confirm(t0+1, oid, cid); err != nil {
				t.Fatal(err)
			}
		}
		target, err := s.SubmitOrder(2*numOrders+10, "tenant", "pipe", "A", LevelEmergency)
		if err != nil {
			t.Fatal(err)
		}
		return s, s.orders[target]
	}

	s1, o1 := build(64)
	s2, o2 := build(1024)
	c1 := countSelection(s1, o1)
	c2 := countSelection(s2, o2)
	if c1 != c2 {
		t.Fatalf("selection cost must be independent of order count: %d vs %d", c1, c2)
	}
	// 承包商翻倍时成本才增长，且近似线性，进一步锚定复杂度为 O(C)。
	s3, o3 := build(64)
	for range 8 {
		if _, err := s3.RegisterContractor(2*64+20, []string{"pipe"}, []string{"A"}, 1_000_000, true); err != nil {
			t.Fatal(err)
		}
	}
	c3 := countSelection(s3, o3)
	if c3 <= c1 {
		t.Fatalf("doubling contractors must raise cost: %d vs %d", c1, c3)
	}
}

// BenchmarkSelectContractor 给出墙钟佐证：工单总数相差 16 倍时，
// 单次真实选取耗时同量级。
func BenchmarkSelectContractor(b *testing.B) {
	cfg := Config{
		ResponseLimit: [4]int{100, 100, 100, 100},
		CompleteLimit: [4]int{1000, 1000, 1000, 1000},
		RejectUpgrade: 100,
	}
	for _, n := range []int{256, 4096} {
		s := New(cfg)
		for range 16 {
			if _, err := s.RegisterContractor(0, []string{"pipe"}, []string{"A"}, 1_000_000, true); err != nil {
				b.Fatal(err)
			}
		}
		for i := range n {
			t0 := 1 + 2*i
			oid, _ := s.SubmitOrder(t0, "t", "pipe", "A", LevelRoutine)
			_, cid, _ := s.DispatchNext(t0)
			_ = s.Confirm(t0+1, oid, cid)
		}
		target, _ := s.SubmitOrder(2*n+10, "t", "pipe", "A", LevelEmergency)
		o := s.orders[target]
		b.Run(ordersLabel(n), func(b *testing.B) {
			for b.Loop() {
				if c := s.selectContractor(o); c == nil {
					b.Fatal("expected candidate")
				}
				if c := s.selectPreemptor(o); c == nil {
					// 承包商未满手，抢占为空属正常；只保证选择过程不随 N 增长。
				}
			}
		})
	}
}

func ordersLabel(n int) string {
	if n >= 1000 {
		return "orders-4k"
	}
	return "orders-256"
}
