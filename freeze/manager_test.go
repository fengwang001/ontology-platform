package freeze

import "testing"

func b(s string) []byte { return []byte(s) }

func mustOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", what, err)
	}
}

func wantReason(t *testing.T, err error, reason Reason, what string) {
	t.Helper()
	if !IsError(err, reason) {
		t.Fatalf("%s: want reason %q, got %v", what, reason, err)
	}
}

func expectOrders(t *testing.T, snap Snapshot, want []EffectiveOrder) {
	t.Helper()
	if len(snap.Orders) != len(want) {
		t.Fatalf("orders len: want %d, got %+v", len(want), snap.Orders)
	}
	for i := range want {
		got := snap.Orders[i]
		if string(got.ID) != string(want[i].ID) ||
			got.Amount != want[i].Amount ||
			got.Effective != want[i].Effective ||
			got.Expire != want[i].Expire {
			t.Fatalf("order[%d]: want %+v, got %+v", i, want[i], got)
		}
	}
}

// 例一（全部 t=0、exp=0）。
func TestExample1(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 60, 0), "freeze o1")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 70, 0), "freeze o2")
	mustOK(t, m.Freeze(acct, 0, b("o3"), 30, 0), "freeze o3")

	snap, err := m.Query(acct, 0)
	mustOK(t, err, "query")
	if snap.Balance != 100 || snap.Available != 0 {
		t.Fatalf("want B=100 V=0, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 60, Effective: 60},
		{ID: b("o2"), Amount: 70, Effective: 40},
		{ID: b("o3"), Amount: 30, Effective: 0},
	})

	mustOK(t, m.Seize(acct, 0, b("o1"), 60), "seize o1 60")
	snap, err = m.Query(acct, 0)
	mustOK(t, err, "query")
	if snap.Balance != 40 || snap.Available != 0 {
		t.Fatalf("want B=40 V=0, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o2"), Amount: 70, Effective: 40},
		{ID: b("o3"), Amount: 30, Effective: 0},
	})

	mustOK(t, m.Unfreeze(acct, 0, b("o2"), 70), "unfreeze o2 all")
	snap, err = m.Query(acct, 0)
	mustOK(t, err, "query")
	if snap.Balance != 40 || snap.Available != 10 {
		t.Fatalf("want B=40 V=10, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o3"), Amount: 30, Effective: 30},
	})
}

// 例二：到期失效、SeizeQ 跨令、编号复用。
func TestExample2(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 60, 0), "freeze o1")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 70, 10), "freeze o2")
	mustOK(t, m.Freeze(acct, 0, b("o3"), 30, 0), "freeze o3")

	snap, err := m.Query(acct, 0)
	mustOK(t, err, "query t=0")
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 60, Effective: 60, Expire: 0},
		{ID: b("o2"), Amount: 70, Effective: 40, Expire: 10},
		{ID: b("o3"), Amount: 30, Effective: 0, Expire: 0},
	})

	// t=9：o2 仍有效。
	snap, err = m.Query(acct, 9)
	mustOK(t, err, "query t=9")
	if snap.Available != 0 {
		t.Fatalf("t=9 V: want 0, got %d", snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 60, Effective: 60, Expire: 0},
		{ID: b("o2"), Amount: 70, Effective: 40, Expire: 10},
		{ID: b("o3"), Amount: 30, Effective: 0, Expire: 0},
	})

	// t=10：o2 恰到期失效，o3 递补。
	snap, err = m.Query(acct, 10)
	mustOK(t, err, "query t=10")
	if snap.Balance != 100 || snap.Available != 10 {
		t.Fatalf("t=10: want B=100 V=10, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 60, Effective: 60, Expire: 0},
		{ID: b("o3"), Amount: 30, Effective: 30, Expire: 0},
	})

	mustOK(t, m.SeizeQueue(acct, 10, 70), "seizeq 70")
	snap, err = m.Query(acct, 10)
	mustOK(t, err, "query after seizeq")
	if snap.Balance != 30 || snap.Available != 10 {
		t.Fatalf("after seizeq: want B=30 V=10, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o3"), Amount: 20, Effective: 20, Expire: 0},
	})

	// 21 > e 之和 20：拒绝，无部分执行。
	err = m.SeizeQueue(acct, 10, 21)
	wantReason(t, err, ReasonSeizeQOver, "seizeq 21")
	snap, err = m.Query(acct, 10)
	mustOK(t, err, "query after reject")
	if snap.Balance != 30 || snap.Available != 10 {
		t.Fatalf("rejected seizeq changed state: B=%d V=%d", snap.Balance, snap.Available)
	}

	// 旧 o2 已失效，编号可复用，排在 o3 之后。
	mustOK(t, m.Freeze(acct, 10, b("o2"), 5, 0), "freeze o2 reuse")
	snap, err = m.Query(acct, 10)
	mustOK(t, err, "query after reuse")
	if snap.Balance != 30 || snap.Available != 5 {
		t.Fatalf("after reuse: want B=30 V=5, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o3"), Amount: 20, Effective: 20, Expire: 0},
		{ID: b("o2"), Amount: 5, Effective: 5, Expire: 0},
	})
}

// exp 恰等于 t 已失效；比 t 大 1 仍有效；Freeze 的 exp 恰等于 t 被拒，大 1 通过。
func TestExpiryBoundary(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 10, 5), "freeze exp=5")

	snap, err := m.Query(acct, 4)
	mustOK(t, err, "query t=4")
	if len(snap.Orders) != 1 || snap.Orders[0].Effective != 10 {
		t.Fatalf("t=4: order should be live, got %+v", snap.Orders)
	}

	err = m.Freeze(acct, 5, b("bad"), 1, 5)
	wantReason(t, err, ReasonInvalidParam, "freeze exp==t")
	mustOK(t, m.Freeze(acct, 5, b("ok"), 1, 6), "freeze exp=t+1")

	// t=5：o1 恰到期；对已失效令 Unfreeze 报令不存在。
	err = m.Unfreeze(acct, 5, b("o1"), 1)
	wantReason(t, err, ReasonOrderMissing, "unfreeze expired")

	snap, err = m.Query(acct, 5)
	mustOK(t, err, "query t=5")
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("ok"), Amount: 1, Effective: 1, Expire: 6},
	})

	// t=6：新令也到期。
	snap, err = m.Query(acct, 6)
	mustOK(t, err, "query t=6")
	if len(snap.Orders) != 0 || snap.Available != 100 {
		t.Fatalf("t=6: want no orders V=100, got %+v V=%d", snap.Orders, snap.Available)
	}
}

// 名义金额大于余额的轮候令；存入使轮候令递补生效。
func TestWaitingOrderBackfillByDeposit(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 50), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 30, 0), "o1 30")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 100, 0), "o2 100 waiting")

	snap, err := m.Query(acct, 0)
	mustOK(t, err, "query")
	if snap.Available != 0 {
		t.Fatalf("V: want 0, got %d", snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 30, Effective: 30},
		{ID: b("o2"), Amount: 100, Effective: 20},
	})

	// 存入 40：o1 已满，o2 有效额从 20 补到 60（名义 100 仍轮候 40），V 仍为 0。
	mustOK(t, m.Deposit(acct, 1, 40), "deposit 40")
	snap, err = m.Query(acct, 1)
	mustOK(t, err, "query after deposit")
	if snap.Balance != 90 || snap.Available != 0 {
		t.Fatalf("want B=90 V=0, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 30, Effective: 30},
		{ID: b("o2"), Amount: 100, Effective: 60},
	})
}

// 前序令到期使后序令递补。
func TestWaitingOrderBackfillByExpiry(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 60, 10), "o1 exp=10")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 100, 0), "o2 waiting")

	snap, err := m.Query(acct, 9)
	mustOK(t, err, "query t=9")
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 60, Effective: 60, Expire: 10},
		{ID: b("o2"), Amount: 100, Effective: 40},
	})

	snap, err = m.Query(acct, 10)
	mustOK(t, err, "query t=10")
	if snap.Available != 0 {
		t.Fatalf("V: want 0, got %d", snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o2"), Amount: 100, Effective: 100},
	})
}

// 后序令的增减不影响前序令的有效冻结额。
func TestLaterOrdersDoNotAffectEarlier(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 60, 0), "o1")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 60, 0), "o2")
	mustOK(t, m.Freeze(acct, 0, b("o3"), 1000, 0), "o3 waiting")

	snap, _ := m.Query(acct, 0)
	if snap.Orders[0].Effective != 60 || snap.Orders[1].Effective != 40 || snap.Orders[2].Effective != 0 {
		t.Fatalf("unexpected effective: %+v", snap.Orders)
	}

	// 给后序追加新令、解冻 o3、部分解冻 o2，均不得改变 o1 的 e。
	mustOK(t, m.Freeze(acct, 0, b("o4"), 900, 0), "o4")
	mustOK(t, m.Unfreeze(acct, 0, b("o3"), 500), "unfreeze o3")
	mustOK(t, m.Unfreeze(acct, 0, b("o2"), 40), "unfreeze o2 part")
	snap, _ = m.Query(acct, 0)
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o1"), Amount: 60, Effective: 60},
		{ID: b("o2"), Amount: 20, Effective: 20},
		{ID: b("o3"), Amount: 500, Effective: 20},
		{ID: b("o4"), Amount: 900, Effective: 0},
	})
}

// Debit 的 x 恰等于 V 通过，比 V 大 1 被拒。
func TestDebitBoundary(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 60, 0), "o1")

	mustOK(t, m.Debit(acct, 0, 40), "debit == V")
	snap, _ := m.Query(acct, 0)
	if snap.Balance != 60 || snap.Available != 0 {
		t.Fatalf("after debit: want B=60 V=0, got B=%d V=%d", snap.Balance, snap.Available)
	}

	err := m.Debit(acct, 0, 1)
	wantReason(t, err, ReasonAvailableShort, "debit V+1")
	snap, _ = m.Query(acct, 0)
	if snap.Balance != 60 {
		t.Fatalf("rejected debit changed B: %d", snap.Balance)
	}
}

// Seize 的 x 恰等于 e_i 通过（构造 e_i < a_i），比 e_i 大 1 被拒。
func TestSeizeBoundary(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 1000, 0), "o1 huge")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 1000, 0), "o2 huge e=0")

	snap, _ := m.Query(acct, 0)
	if snap.Orders[0].Effective != 100 || snap.Orders[1].Effective != 0 {
		t.Fatalf("setup wrong: %+v", snap.Orders)
	}

	mustOK(t, m.Seize(acct, 0, b("o1"), 100), "seize == e")
	snap, _ = m.Query(acct, 0)
	if snap.Balance != 0 || snap.Orders[0].Amount != 900 || snap.Orders[0].Effective != 0 {
		t.Fatalf("after seize: %+v B=%d", snap.Orders, snap.Balance)
	}

	err := m.Seize(acct, 0, b("o1"), 1)
	wantReason(t, err, ReasonSeizeOver, "seize e+1")
	// 对 e=0 的令扣 1 同样超额。
	err = m.Seize(acct, 0, b("o2"), 1)
	wantReason(t, err, ReasonSeizeOver, "seize zero-effective")
}

// SeizeQ x 恰等于 e 之和：所有令 e 扣尽；大 1 被拒且不部分执行。
func TestSeizeQueueExactAndOver(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 40, 0), "o1")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 1000, 0), "o2 e=60")

	mustOK(t, m.SeizeQueue(acct, 0, 100), "seizeq == total e")
	snap, _ := m.Query(acct, 0)
	if snap.Balance != 0 || snap.Available != 0 {
		t.Fatalf("want B=0 V=0, got B=%d V=%d", snap.Balance, snap.Available)
	}
	// o1 名义与有效均扣尽 -> 移出；o2 名义 940、e=0，留在队列。
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o2"), Amount: 940, Effective: 0},
	})

	// 再存入 10 后 e 之和为 10。
	mustOK(t, m.Deposit(acct, 1, 10), "deposit 10")
	err := m.SeizeQueue(acct, 1, 11)
	wantReason(t, err, ReasonSeizeQOver, "seizeq total+1")
	snap, _ = m.Query(acct, 1)
	if snap.Balance != 10 || snap.Orders[0].Amount != 940 || snap.Orders[0].Effective != 10 {
		t.Fatalf("rejected seizeq partially applied: %+v B=%d", snap.Orders, snap.Balance)
	}
}

// SeizeQ 跨多道令且末道只部分扣划。
func TestSeizeQueueAcrossOrders(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 30, 0), "o1 a=30 e=30")
	mustOK(t, m.Freeze(acct, 0, b("o2"), 30, 0), "o2 a=30 e=30")
	mustOK(t, m.Freeze(acct, 0, b("o3"), 100, 0), "o3 a=100 e=40")

	// 扣 80：o1 全扣 30 移出，o2 全扣 30 移出，o3 部分扣 20（a 余 80）。
	mustOK(t, m.SeizeQueue(acct, 0, 80), "seizeq 80")
	snap, _ := m.Query(acct, 0)
	if snap.Balance != 20 || snap.Available != 0 {
		t.Fatalf("want B=20 V=0, got B=%d V=%d", snap.Balance, snap.Available)
	}
	expectOrders(t, snap, []EffectiveOrder{
		{ID: b("o3"), Amount: 80, Effective: 20},
	})
}

// Unfreeze x 恰等于 a_i 整令移出；大 1 被拒。
func TestUnfreezeBoundary(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 0, 100), "deposit")
	mustOK(t, m.Freeze(acct, 0, b("o1"), 30, 0), "o1")

	err := m.Unfreeze(acct, 0, b("o1"), 31)
	wantReason(t, err, ReasonUnfreezeOver, "unfreeze a+1")
	snap, _ := m.Query(acct, 0)
	if len(snap.Orders) != 1 || snap.Orders[0].Amount != 30 {
		t.Fatalf("rejected unfreeze changed state: %+v", snap.Orders)
	}

	mustOK(t, m.Unfreeze(acct, 0, b("o1"), 30), "unfreeze == a")
	snap, _ = m.Query(acct, 0)
	if len(snap.Orders) != 0 || snap.Available != 100 {
		t.Fatalf("want order removed V=100, got %+v V=%d", snap.Orders, snap.Available)
	}
}

// 时序倒退被拒且不推进 m；参数非法先于时序判定；拒绝不改任何状态。
func TestRejectionsDoNotAdvanceOrMutate(t *testing.T) {
	m := NewManager()
	acct := b("acc")
	mustOK(t, m.Deposit(acct, 5, 100), "deposit at t=5")
	mustOK(t, m.Freeze(acct, 5, b("o1"), 60, 0), "freeze")

	// 时序倒退。
	_, err := m.Query(acct, 4)
	wantReason(t, err, ReasonTimeRegression, "query t=4")
	wantReason(t, m.Debit(acct, 4, 1), ReasonTimeRegression, "debit t=4")
	wantReason(t, m.SeizeQueue(acct, 4, 1), ReasonTimeRegression, "seizeq t=4")

	// 倒退被拒后，t=5 的同刻操作仍可用（m 未被推进也未被倒退污染）。
	mustOK(t, m.Debit(acct, 5, 40), "debit at t=5 still works")

	// 参数非法先于时序倒退上报。
	wantReason(t, m.Debit(acct, 4, 0), ReasonInvalidParam, "x=0 even though t<m")
	_, err = m.Query(b(""), 4)
	wantReason(t, err, ReasonInvalidParam, "empty acct even though t<m")

	// 账户不存在（Deposit 除外）。
	_, err = m.Query(b("nope"), 5)
	wantReason(t, err, ReasonAccountMissing, "query missing")
	wantReason(t, m.Debit(b("nope"), 5, 1), ReasonAccountMissing, "debit missing")
	wantReason(t, m.Freeze(b("nope"), 5, b("z"), 1, 0), ReasonAccountMissing, "freeze missing")
	wantReason(t, m.Unfreeze(b("nope"), 5, b("z"), 1), ReasonAccountMissing, "unfreeze missing")
	wantReason(t, m.Seize(b("nope"), 5, b("z"), 1), ReasonAccountMissing, "seize missing")
	wantReason(t, m.SeizeQueue(b("nope"), 5, 1), ReasonAccountMissing, "seizeq missing")

	// 令编号重复在失效清理之后判定；对不存在的令操作报令不存在。
	wantReason(t, m.Freeze(acct, 5, b("o1"), 1, 0), ReasonIDDuplicate, "dup id")
	wantReason(t, m.Unfreeze(acct, 5, b("ghost"), 1), ReasonOrderMissing, "unfreeze no order")
	wantReason(t, m.Seize(acct, 5, b("ghost"), 1), ReasonOrderMissing, "seize no order")

	// 各种参数非法。
	wantReason(t, m.Deposit(acct, 5, 0), ReasonInvalidParam, "x=0")
	wantReason(t, m.Deposit(acct, -1, 1), ReasonInvalidParam, "t<0")
	wantReason(t, m.Deposit(acct, MaxTime+1, 1), ReasonInvalidParam, "t>1e12")
	wantReason(t, m.Deposit(acct, 5, MaxAmount+1), ReasonInvalidParam, "x>1e12")
	wantReason(t, m.Freeze(acct, 5, b(""), 1, 0), ReasonInvalidParam, "empty id")
	wantReason(t, m.Freeze(acct, 5, b("x"), 1, MaxTime+1), ReasonInvalidParam, "exp>1e12")

	// 存入后 B 超 1e15：参数非法拒绝且 B 不变。
	big := b("big")
	for i := 0; i < int(MaxBalance/MaxAmount); i++ {
		mustOK(t, m.Deposit(big, 5, MaxAmount), "deposit to cap")
	}
	wantReason(t, m.Deposit(big, 5, 1), ReasonInvalidParam, "over cap")
	snap, _ := m.Query(big, 5)
	if snap.Balance != MaxBalance {
		t.Fatalf("over-cap deposit changed B: %d", snap.Balance)
	}

	// 主账户状态：B=60（100-40），o1 e=60, V=0。
	snap, _ = m.Query(acct, 5)
	if snap.Balance != 60 || snap.Available != 0 || len(snap.Orders) != 1 ||
		snap.Orders[0].Effective != 60 {
		t.Fatalf("state drifted after rejections: B=%d V=%d orders=%+v",
			snap.Balance, snap.Available, snap.Orders)
	}
}

// 全局守恒：sum(B) + seized + debited == deposited。
func TestConservation(t *testing.T) {
	m := NewManager()
	mustOK(t, m.Deposit(b("a"), 0, 100), "d1")
	mustOK(t, m.Deposit(b("b"), 0, 50), "d2")
	mustOK(t, m.Freeze(b("a"), 0, b("o"), 80, 0), "f")
	mustOK(t, m.Seize(b("a"), 0, b("o"), 30), "seize")
	mustOK(t, m.Debit(b("b"), 0, 20), "debit")

	m.mu.Lock()
	total := int64(0)
	for _, acc := range m.accounts {
		total += acc.balance
	}
	lhs := total + m.seizedTotal + m.debitedTotal
	rhs := m.depositedTotal
	m.mu.Unlock()
	if lhs != rhs {
		t.Fatalf("conservation broken: balances+seized+debited=%d deposited=%d", lhs, rhs)
	}
}
