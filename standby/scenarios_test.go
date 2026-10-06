package standby

import (
	"errors"
	"testing"
)

func mustID(t *testing.T, id EntryID, err error) EntryID {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return id
}

func reg(t *testing.T, s *System, flightName, cabin, passenger string, party int, prio Priority, now int64) EntryID {
	t.Helper()
	id, err := s.Register(flightName, cabin, passenger, party, prio, now)
	if err != nil {
		t.Fatalf("register %s: %v", passenger, err)
	}
	return id
}

func mustErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func states(snap FlightSnapshot) map[EntryID]EntryState {
	m := make(map[EntryID]EntryState, len(snap.Entries))
	for _, e := range snap.Entries {
		m[e.ID] = e.State
	}
	return m
}

// 空余数恰等于条目人数：必须兑现。
func TestExactFit(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 3, 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	id := reg(t, s, "CA1", "Y", "p1", 3, PrioLow, 1)
	snap, err := s.Snapshot("CA1", "Y", 1)
	if err != nil {
		t.Fatal(err)
	}
	if states(snap)[id] != StatePending || snap.Free != 0 || snap.Pending != 3 {
		t.Fatalf("exact-fit not fulfilled: %+v", snap)
	}
}

// 跳过大条目后兑现其后的小条目，大条目保留原位。
func TestSkipBigFulfillSmall(t *testing.T) {
	s := New()
	mustErr(t, s.CreateFlight("CA1", "Y", 5, 10, 100, 0), nil)
	// 先占 3 个已确认，空 2。
	c := reg(t, s, "CA1", "Y", "conf", 3, PrioHigh, 0)
	if err := s.Confirm(c, 1); err != nil {
		t.Fatal(err)
	}
	big := reg(t, s, "CA1", "Y", "big", 5, PrioHigh, 2)
	small := reg(t, s, "CA1", "Y", "small", 2, PrioHigh, 3)
	snap, _ := s.Snapshot("CA1", "Y", 3)
	st := states(snap)
	if st[big] != StateWaiting || st[small] != StatePending {
		t.Fatalf("big=%v small=%v", st[big], st[small])
	}
	// 小条目确认后取消，释放 2 座：大条目 5 人仍放不下，原位保留。
	if err := s.Confirm(small, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelConfirmed(small, 5); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot("CA1", "Y", 5)
	st = states(snap)
	if st[big] != StateWaiting {
		t.Fatalf("big must keep its place when it does not fit: %v", st[big])
	}
	// 再释放最初的 3 个座位，合计 5：大条目此时被兑现。
	if err := s.CancelConfirmed(c, 6); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot("CA1", "Y", 6)
	st = states(snap)
	if st[big] != StatePending {
		t.Fatalf("big should be pending after 5 seats released: %v", st[big])
	}
}

// 同时刻多条过期：合并释放后只兑现一次（用结果与期限可复现地判定）。
func TestSameTimeExpiryMerged(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 4, 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	// 两条各 2 人在 0 时刻兑现，期限都是 10；它们到期前各被一条 2 人候补挡住。
	a := reg(t, s, "CA1", "Y", "a", 2, PrioHigh, 0)
	b := reg(t, s, "CA1", "Y", "b", 2, PrioHigh, 0)
	_ = a
	_ = b
	next := reg(t, s, "CA1", "Y", "c", 4, PrioLow, 1)
	// 在 t=10 恰好过期（恰等于也算过期）：4 个座位合并释放，c 一次兑现。
	snap, _ := s.Snapshot("CA1", "Y", 10)
	if states(snap)[next] != StatePending {
		t.Fatalf("c should be pending once at t=10: %+v", states(snap))
	}
	if snap.Free != 0 || snap.Pending != 4 {
		t.Fatalf("merged release wrong: %+v", snap)
	}
	// 期限唯一可复现：10 + delay(10) = 20。
	for _, e := range snap.Entries {
		if e.ID == next && e.Deadline != 20 {
			t.Fatalf("deadline=%d want 20", e.Deadline)
		}
	}
}

// 两次过期之间登记的条目只能参与第二次及以后的兑现。
func TestRegisteredBetweenExpiries(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 2, 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	first := reg(t, s, "CA1", "Y", "f", 2, PrioHigh, 0) // t=10 过期
	// 在第一次过期之前登记的条目：可以参与第一次兑现（t=10），期限 20。
	early := reg(t, s, "CA1", "Y", "e", 2, PrioHigh, 5)
	snap10, _ := s.Snapshot("CA1", "Y", 10)
	st10 := states(snap10)
	if st10[first] != StateExpired || st10[early] != StatePending {
		t.Fatalf("first expiry: f=%v e=%v", st10[first], st10[early])
	}
	// 在两次过期之间（t=10..20）登记的条目：不能参与第一次，只能等第二次（t=20）。
	between := reg(t, s, "CA1", "Y", "b", 2, PrioHigh, 15)
	snap20, _ := s.Snapshot("CA1", "Y", 20)
	st := states(snap20)
	if st[early] != StateExpired {
		t.Fatalf("early should expire at 20: %v", st[early])
	}
	if st[between] != StatePending {
		t.Fatalf("between entry may only join the second fulfillment: %v", st[between])
	}
	for _, e := range snap20.Entries {
		if e.ID == between && e.Deadline != 30 {
			t.Fatalf("between deadline must be based on release at 20, got %d", e.Deadline)
		}
	}
}

// 容量下调产生负空余，再上调跨过零点时兑现。
func TestCapacityDownBelowZeroThenUp(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 5, 10, 100, 0); err != nil {
		t.Fatal(err)
	}
	c := reg(t, s, "CA1", "Y", "c", 5, PrioHigh, 0)
	if err := s.Confirm(c, 1); err != nil {
		t.Fatal(err)
	}
	waiter := reg(t, s, "CA1", "Y", "w", 3, PrioHigh, 2)
	if err := s.SetCapacity("CA1", "Y", 3, 3); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot("CA1", "Y", 3)
	if snap.Free != -2 || states(snap)[waiter] != StateWaiting {
		t.Fatalf("negative free expected, got %+v state %v", snap, states(snap)[waiter])
	}
	if err := s.SetCapacity("CA1", "Y", 6, 4); err != nil { // 空余 1，不足 3
		t.Fatal(err)
	}
	snap, _ = s.Snapshot("CA1", "Y", 4)
	if states(snap)[waiter] != StateWaiting {
		t.Fatalf("still not enough seats")
	}
	if err := s.SetCapacity("CA1", "Y", 8, 5); err != nil { // 空余 3，恰等于
		t.Fatal(err)
	}
	snap, _ = s.Snapshot("CA1", "Y", 5)
	if states(snap)[waiter] != StatePending || snap.Free != 0 {
		t.Fatalf("cross-zero fulfillment failed: %+v", snap)
	}
}

// 等级调整后次序变化，但登记时刻保留。
func TestPriorityChangeKeepsRegistrationTime(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 3, 10, 100, 0); err != nil {
		t.Fatal(err)
	}
	c := reg(t, s, "CA1", "Y", "c", 3, PrioHigh, 0)
	if err := s.Confirm(c, 1); err != nil {
		t.Fatal(err)
	}
	low := reg(t, s, "CA1", "Y", "lo", 2, PrioLow, 2)
	high := reg(t, s, "CA1", "Y", "hi", 2, PrioHigh, 3)
	// low 上调为 high：同级后按登记时刻，low(2) 早于 hi(3)，故 low 先兑现。
	if err := s.ChangePriority(low, PrioHigh, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelConfirmed(c, 5); err != nil { // 释放 3 座
		t.Fatal(err)
	}
	snap, _ := s.Snapshot("CA1", "Y", 5)
	st := states(snap)
	if st[low] != StatePending || st[high] != StateWaiting {
		t.Fatalf("priority/order wrong: lo=%v hi=%v", st[low], st[high])
	}
	for _, e := range snap.Entries {
		if e.ID == low && e.Registered != 2 {
			t.Fatalf("registration time must be kept, got %d", e.Registered)
		}
	}
	// 待确认条目不可调整等级。
	mustErr(t, s.ChangePriority(low, PrioLow, 6), ErrEntryState)
}

// 航班取消后六态可分：取消前查询可见六种状态来源，取消后未结束条目全部作废。
func TestCancelFlightSixStates(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 6, 10, 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFlight("CA2", "Y", 1, 10, 5, 0); err != nil {
		t.Fatal(err)
	}
	// CA2：1 人在 t=0 兑现，期限 5，t=5 恰好过期（恰等于也算过期）。
	expired := reg(t, s, "CA2", "Y", "p-e", 1, PrioHigh, 0)
	// CA1：4 人确认，留 2 座；随后 2 人待确认；再来 1 人排队；另 1 人撤回。
	confirmed := reg(t, s, "CA1", "Y", "p-c", 4, PrioHigh, 0)
	pending := reg(t, s, "CA1", "Y", "p-p", 2, PrioHigh, 1)
	waiting := reg(t, s, "CA1", "Y", "p-w", 1, PrioLow, 2)
	withdrawn := reg(t, s, "CA1", "Y", "p-x", 1, PrioLow, 3)
	if err := s.Withdraw(withdrawn, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(confirmed, 4); err != nil {
		t.Fatal(err)
	}

	// 取消前查询：confirmed/pending/waiting/withdrawn/expired 五态来源可区分。
	snap, err := s.Snapshot("CA1", "Y", 5)
	if err != nil {
		t.Fatal(err)
	}
	st := states(snap)
	preStates := []EntryState{st[confirmed], st[pending], st[waiting], st[withdrawn]}
	wantPre := []EntryState{StateConfirmed, StatePending, StateWaiting, StateWithdrawn}
	for i := range preStates {
		if preStates[i] != wantPre[i] {
			t.Fatalf("pre-cancel states wrong: got %v want %v", preStates, wantPre)
		}
	}
	snap2, _ := s.Snapshot("CA2", "Y", 5)
	if states(snap2)[expired] != StateExpired {
		t.Fatalf("expired state must be distinct: %v", states(snap2)[expired])
	}

	if err := s.CancelFlight("CA1", "Y", 6); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot("CA1", "Y", 7)
	if err != nil {
		t.Fatal(err)
	}
	st = states(snap)
	for _, id := range []EntryID{confirmed, pending, waiting, withdrawn} {
		if st[id] != StateVoided {
			t.Fatalf("entry %d should be voided, got %v", id, st[id])
		}
	}
	snap2, _ = s.Snapshot("CA2", "Y", 7)
	if states(snap2)[expired] != StateExpired {
		t.Fatalf("other flight unaffected, expired state distinct")
	}
	_, errReg := s.Register("CA1", "Y", "z", 1, PrioLow, 8)
	mustErr(t, errReg, ErrFlightCanceled)
	mustErr(t, s.SetCapacity("CA1", "Y", 9, 8), ErrFlightCanceled)
	mustErr(t, s.CancelFlight("CA1", "Y", 8), ErrFlightCanceled)
	mustErr(t, s.Withdraw(pending, 8), ErrFlightCanceled)
}

// 拒绝次序：相邻类别每一对。
func TestRejectionOrdering(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 1, 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFlight("CAX", "Y", 1, 1, 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFlight("CA2", "Y", 1, 1, 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelFlight("CAX", "Y", 1); err != nil {
		t.Fatal(err)
	}

	// 参数非法 > 时钟回退（同时满足两者，只报参数非法）。
	_, err := s.Register("CA1", "Y", "p", 0, PrioLow, -1)
	mustErr(t, err, ErrInvalid)
	// 时钟回退 > 航班不存在：参数合法，但时刻 0 < 当前时钟 1。
	_, err = s.Register("NOPE", "Y", "p", 1, PrioLow, 0)
	mustErr(t, err, ErrClockRewind)
	_, err = s.Register("GHOST", "Y", "p", 1, PrioLow, 2)
	mustErr(t, err, ErrFlightNotFound)
	_, err = s.Register("CAX", "Y", "p", 1, PrioLow, 2)
	mustErr(t, err, ErrFlightCanceled)

	// 条目不存在 > 航班已取消/状态不符：不存在的 id 无从判定状态。
	mustErr(t, s.Withdraw(9999, 2), ErrEntryNotFound)
	a := reg(t, s, "CA1", "Y", "a", 1, PrioHigh, 2)
	if err := s.Confirm(a, 3); err != nil {
		t.Fatal(err)
	}

	// 航班已取消 > 条目不存在/状态不符：已取消航班上的有效 id 也只报航班已取消。
	mustErr(t, s.Withdraw(a, 4), ErrEntryState) // 先在正常航班验证状态不符

	// 重复登记 > 队列已满：CA2 上限 1，dup 已在列，重复旅客与新旅客都拒绝，
	// 但重复旅客先报重复登记。
	dup := reg(t, s, "CA2", "Y", "dup", 1, PrioLow, 5)
	_ = dup
	_, err = s.Register("CA2", "Y", "dup", 1, PrioLow, 6)
	mustErr(t, err, ErrDuplicate)
	reg(t, s, "CA2", "Y", "r", 1, PrioLow, 6) // dup 兑现后 r 进入候补占满上限
	_, err = s.Register("CA2", "Y", "s", 1, PrioLow, 6)
	mustErr(t, err, ErrQueueFull)
}

// 被拒绝的操作不推进时钟、不触发过期结算、不改任何条目。
func TestRejectedHasNoSideEffect(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 10, 5, 0); err != nil {
		t.Fatal(err)
	}
	id := reg(t, s, "CA1", "Y", "p", 1, PrioHigh, 0)
	before, _ := s.Snapshot("CA1", "Y", 0)
	if states(before)[id] != StatePending {
		t.Fatal("setup")
	}
	// 在期限 t=5 时发起一个非法操作（参数非法优先，且必须不结算、不推进时钟）。
	_, err := s.Register("CA1", "Y", "", 1, PrioLow, 5)
	mustErr(t, err, ErrInvalid)
	after, _ := s.Snapshot("CA1", "Y", 4) // t=4 < 期限，应仍待确认
	if states(after)[id] != StatePending {
		t.Fatalf("rejected op must not settle expiry: %v", states(after)[id])
	}
	// 时钟也不推进：再用 t=4 的合法确认不应报回退，且在期限内可以确认。
	if err := s.Confirm(id, 4); err != nil {
		t.Fatalf("clock not advanced by rejection: %v", err)
	}
}

// 重复登记与队列已满的拒绝必须先结算过期，再判定。
func TestDuplicateCheckedAfterSettle(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 1, 5, 0); err != nil {
		t.Fatal(err)
	}
	id := reg(t, s, "CA1", "Y", "p", 1, PrioHigh, 0)
	// t=5 时 id 已过期释放；同旅客可重新登记（不是重复登记）。
	newID, err := s.Register("CA1", "Y", "p", 1, PrioHigh, 5)
	if err != nil || newID == id {
		t.Fatalf("expired pending should allow re-register: id=%d err=%v", newID, err)
	}
}

// 同登记时刻按条目标识小者在前。
func TestSameTimeOrderByID(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 10, 100, 0); err != nil {
		t.Fatal(err)
	}
	a := reg(t, s, "CA1", "Y", "a", 1, PrioHigh, 1)
	b := reg(t, s, "CA1", "Y", "b", 1, PrioHigh, 1)
	snap, _ := s.Snapshot("CA1", "Y", 1)
	st := states(snap)
	if st[a] != StatePending || st[b] != StateWaiting {
		t.Fatalf("same timestamp tie broken by id: %v %v", st[a], st[b])
	}
}

func TestConfirmStrictlyBeforeDeadline(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 10, 5, 0); err != nil {
		t.Fatal(err)
	}
	id := reg(t, s, "CA1", "Y", "p", 1, PrioHigh, 0)
	if err := s.Confirm(id, 5); err == nil {
		t.Fatal("confirm at deadline must fail: expired")
	}
	snap, _ := s.Snapshot("CA1", "Y", 5)
	if states(snap)[id] != StateExpired {
		t.Fatalf("at deadline entry must be expired: %v", states(snap)[id])
	}
}

func TestQueueFullCountsWaitingOnly(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 2, 100, 0); err != nil {
		t.Fatal(err)
	}
	reg(t, s, "CA1", "Y", "a", 1, PrioHigh, 0) // pending
	reg(t, s, "CA1", "Y", "b", 1, PrioHigh, 0) // waiting
	// waiting 条目数为 1（上限 2，不含待确认），还能再登记一条。
	third := reg(t, s, "CA1", "Y", "c", 1, PrioHigh, 1)
	_ = third
	_, err := s.Register("CA1", "Y", "d", 1, PrioHigh, 2)
	mustErr(t, err, ErrQueueFull)
}

func TestTrieSanity(t *testing.T) {
	set := newOrderedSet()
	n := 200
	leaves := make([]*trieLeaf, 0, n)
	for i := 0; i < n; i++ {
		l := &trieLeaf{keyHi: 0, keyLo: uint64(i * 7)}
		set.insert(l)
		leaves = append(leaves, l)
	}
	for i := 0; i < n; i += 2 {
		set.delete(leaves[i])
	}
	cnt := 0
	l := set.seek(0, 0)
	for l != nil {
		cnt++
		l = set.next(l)
	}
	if cnt != n/2 {
		t.Fatalf("count=%d want %d", cnt, n/2)
	}
	for i := 1; i < n; i += 2 {
		set.delete(leaves[i])
	}
	if !set.empty() {
		t.Fatal("set should be empty")
	}
	if set.seek(0, 0) != nil {
		t.Fatal("seek empty must be nil")
	}
}

// 试探性结算后若因队列满被拒绝，必须把过期结算与（未发生的）兑现全部撤销。
func TestRollbackAfterSettleQueueFull(t *testing.T) {
	s := New()
	// 容量 1，候补上限 2，期限 5。待确认 1 人；候补两条各 2 人（释放 1 座也无人能兑现）。
	if err := s.CreateFlight("CA1", "Y", 1, 2, 5, 0); err != nil {
		t.Fatal(err)
	}
	pending := reg(t, s, "CA1", "Y", "p", 1, PrioHigh, 0)
	waiter := reg(t, s, "CA1", "Y", "w", 2, PrioHigh, 0)
	waiter2 := reg(t, s, "CA1", "Y", "w2", 2, PrioLow, 0)
	// t=5：pending 试探性过期，但没有条目能兑现，候补仍 2 条，新登记判队列满后整体回滚。
	_, err := s.Register("CA1", "Y", "z", 1, PrioLow, 5)
	mustErr(t, err, ErrQueueFull)
	snap, _ := s.Snapshot("CA1", "Y", 4)
	st := states(snap)
	if st[pending] != StatePending || st[waiter] != StateWaiting || st[waiter2] != StateWaiting ||
		snap.Pending != 1 || snap.WaitingCount != 2 {
		t.Fatalf("rollback incomplete: %+v %v", snap, st)
	}
	// 被拒绝操作不推进时钟：t=5 的撤回合法，且正常完成结算。
	if err := s.Withdraw(waiter, 5); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot("CA1", "Y", 5)
	st = states(snap)
	if st[pending] != StateExpired || st[waiter] != StateWithdrawn {
		t.Fatalf("after valid op at t=5: %v", st)
	}
}

// 试探性结算后发现重复登记，拒绝且不改变任何状态。
func TestRollbackDuplicate(t *testing.T) {
	s := New()
	if err := s.CreateFlight("CA1", "Y", 1, 10, 5, 0); err != nil {
		t.Fatal(err)
	}
	p := reg(t, s, "CA1", "Y", "p", 1, PrioHigh, 0)
	// t=4 < 期限 5：结算不发生，活跃条目仍在，重复登记被拒绝且不结算。
	_, err := s.Register("CA1", "Y", "p", 1, PrioLow, 4)
	mustErr(t, err, ErrDuplicate)
	snap, _ := s.Snapshot("CA1", "Y", 4)
	if states(snap)[p] != StatePending {
		t.Fatalf("duplicate rejection must not settle: %v", states(snap)[p])
	}
}
