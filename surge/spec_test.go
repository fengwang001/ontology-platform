package surge

import "testing"

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func assertCode(t *testing.T, err error, want ErrorCode, ctx string) {
	t.Helper()
	if CodeOf(err) != want {
		t.Fatalf("%s: want code %s, got %v", ctx, want, err)
	}
}

// TestThresholdExact 比率恰等于阈值：取等号 >=，升档。
func TestThresholdExact(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.RiderOnline(0, "r1", "A"), "online")
	mustOK(t, s.CreateOrder(1, "o1", "A"), "create1")
	mustOK(t, s.CreateOrder(1, "o2", "A"), "create2")
	// pending=2, capacity=1 => ratio=2 == thresholds[1] => 档2
	tier, err := s.Evaluate(5, "A")
	mustOK(t, err, "eval")
	if tier != 2 {
		t.Fatalf("exact threshold: want tier 2, got %d", tier)
	}
}

// TestInfiniteRatio 可用运力为零且有待派订单 => 无穷大 => 最高档。
func TestInfiniteRatio(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.CreateOrder(1, "o1", "A"), "create")
	tier, err := s.Evaluate(5, "A")
	mustOK(t, err, "eval")
	if tier != 3 {
		t.Fatalf("infinite ratio: want top tier 3, got %d", tier)
	}
}

// TestZeroOverZero 运力与待派皆为零 => 比率 0 => 基础档。
func TestZeroOverZero(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	tier, err := s.Evaluate(5, "A")
	mustOK(t, err, "eval")
	if tier != 0 {
		t.Fatalf("0/0: want tier 0, got %d", tier)
	}
}

// TestUpSkipsTiers 上调可一次跨多档。
func TestUpSkipsTiers(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	for _, o := range []OrderID{"o1", "o2", "o3", "o4", "o5"} {
		mustOK(t, s.CreateOrder(0, o, "A"), "create")
	}
	mustOK(t, s.RiderOnline(1, "r1", "A"), "online") // pending=5 cap=1 ratio=5
	tier, err := s.Evaluate(5, "A")
	mustOK(t, err, "eval")
	if tier != 3 {
		t.Fatalf("up skip: want tier 3, got %d", tier)
	}
	ev, _ := s.TierEvents("A")
	if len(ev) != 1 || ev[0].FromTier != 0 || ev[0].ToTier != 3 {
		t.Fatalf("up skip event: %+v", ev)
	}
}

// TestDownOneStepEachTime 下调每次只降一档，且需连续确认 DownConfirmations 次。
func TestDownOneStepEachTime(t *testing.T) {
	s, _ := NewSystem(testCfg()) // 需要 2 次连续确认
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.CreateOrder(0, "o1", "A"), "create")
	_, err := s.Evaluate(0, "A")
	mustOK(t, err, "eval up") // cap=0 => 档3

	mustOK(t, s.RiderOnline(5, "r1", "A"), "online") // cap=1 pending=1 ratio=1
	tr, err := s.Evaluate(5, "A")
	mustOK(t, err, "eval1")
	if tr != 3 {
		t.Fatalf("first down confirm: tier stays 3, got %d", tr)
	}
	tr, err = s.Evaluate(10, "A")
	mustOK(t, err, "eval2")
	if tr != 2 {
		t.Fatalf("after 2 confirms: drop exactly one to 2, got %d", tr)
	}
	tr, _ = s.Evaluate(15, "A")
	if tr != 2 {
		t.Fatalf("counter reset: tier stays 2, got %d", tr)
	}
	tr, _ = s.Evaluate(20, "A")
	if tr != 1 {
		t.Fatalf("next drop one to 1, got %d", tr)
	}
}

// TestDownInterruptedByUp 下调确认中途被打断（先等目标档），计数清零。
func TestDownInterruptedByUp(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.CreateOrder(0, "o1", "A"), "c1")
	_, err := s.Evaluate(0, "A")
	mustOK(t, err, "up3")
	mustOK(t, s.RiderOnline(5, "r1", "A"), "on")
	_, err = s.Evaluate(5, "A")
	mustOK(t, err, "down confirm 1")
	// 骑手下线 => cap=0 目标档3 == 当前档3 => 等于当前档，计数清零。
	mustOK(t, s.RiderOffline(10, "r1"), "off")
	tr, err := s.Evaluate(10, "A")
	mustOK(t, err, "equal-eval")
	if tr != 3 {
		t.Fatalf("equal target keeps tier 3, got %d", tr)
	}
	mustOK(t, s.RiderOnline(15, "r1", "A"), "on2")
	tr, _ = s.Evaluate(15, "A")
	if tr != 3 {
		t.Fatalf("single confirm after reset must not drop, got %d", tr)
	}

	// 真正的“上调打断”：让档位降到 2，累计 1 次向 1 的确认，
	// 然后目标跳回高档（cap=0），立即升回 3，计数清零。
	_, err = s.Evaluate(20, "A")
	mustOK(t, err, "confirm2 -> 2")
	_, err = s.Evaluate(25, "A")
	mustOK(t, err, "1 confirm toward 1")
	mustOK(t, s.RiderOffline(30, "r1"), "off -> inf target")
	tr, err = s.Evaluate(30, "A")
	mustOK(t, err, "up interrupt")
	if tr != 3 {
		t.Fatalf("up interrupt jumps back to 3, got %d", tr)
	}
	snap := s.Snapshot()
	if snap.Regions["A"].DownConfirmed != 0 {
		t.Fatalf("up must clear down counter, got %d", snap.Regions["A"].DownConfirmed)
	}
}

// TestEqualTargetResetsCounter 目标档等于当前档 => 下调确认计数清零。
func TestEqualTargetResetsCounter(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.CreateOrder(0, "o1", "A"), "c1")
	_, err := s.Evaluate(0, "A")
	mustOK(t, err, "up3")
	mustOK(t, s.RiderOnline(5, "r1", "A"), "on")
	_, err = s.Evaluate(5, "A")
	mustOK(t, err, "down1")
	mustOK(t, s.RiderOffline(10, "r1"), "off")
	_, err = s.Evaluate(10, "A")
	mustOK(t, err, "equal reset")
	mustOK(t, s.RiderOnline(15, "r1", "A"), "on2")
	tr, _ := s.Evaluate(15, "A")
	if tr != 3 {
		t.Fatalf("single confirm after reset must not drop, got %d", tr)
	}
}

// TestHoldFullRemovesCapacity 持单达上限使运力减一；完成后重新计入。
func TestHoldFullRemovesCapacity(t *testing.T) {
	s, _ := NewSystem(testCfg()) // MaxHeld=2
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.RiderOnline(0, "r1", "A"), "on")
	mustOK(t, s.CreateOrder(1, "o1", "A"), "c1")
	mustOK(t, s.CreateOrder(1, "o2", "A"), "c2")
	mustOK(t, s.DispatchOrder(2, "o1", "r1"), "d1") // held=1 仍可用
	if c := s.Snapshot().Regions["A"].Capacity; c != 1 {
		t.Fatalf("held1: capacity want 1, got %d", c)
	}
	mustOK(t, s.DispatchOrder(3, "o2", "r1"), "d2") // held=2 满 => 运力 0
	if c := s.Snapshot().Regions["A"].Capacity; c != 0 {
		t.Fatalf("held full: capacity want 0, got %d", c)
	}
	amt, err := s.CompleteOrder(4, "o1")
	mustOK(t, err, "complete")
	if amt != 0 {
		t.Fatalf("base tier subsidy want 0, got %d", amt)
	}
	if c := s.Snapshot().Regions["A"].Capacity; c != 1 {
		t.Fatalf("after complete: capacity back to 1, got %d", c)
	}
}

// TestFullRiderOfflineOnlineCapacity 满持单骑手下线后重新上线不得重复计运力。
func TestFullRiderOfflineOnlineCapacity(t *testing.T) {
	s, _ := NewSystem(testCfg()) // MaxHeld=2
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.RiderOnline(0, "r1", "A"), "on")
	mustOK(t, s.CreateOrder(1, "o1", "A"), "c1")
	mustOK(t, s.CreateOrder(1, "o2", "A"), "c2")
	mustOK(t, s.DispatchOrder(2, "o1", "r1"), "d1")
	mustOK(t, s.DispatchOrder(3, "o2", "r1"), "d2") // 满，运力 0
	mustOK(t, s.RiderOffline(4, "r1"), "off full")  // 下线不改运力计数
	if c := s.Snapshot().Regions["A"].Capacity; c != 0 {
		t.Fatalf("offline full rider: capacity stays 0, got %d", c)
	}
	mustOK(t, s.RiderOnline(5, "r1", "A"), "re-online full")
	if c := s.Snapshot().Regions["A"].Capacity; c != 0 {
		t.Fatalf("re-online full rider must not count, got %d", c)
	}
	// 完成一单释放名额后才重新计入。
	_, err := s.CompleteOrder(6, "o1")
	mustOK(t, err, "complete")
	if c := s.Snapshot().Regions["A"].Capacity; c != 1 {
		t.Fatalf("after release: capacity 1, got %d", c)
	}
}

// TestLockedTierStable 档位变化不影响已锁定订单，补贴只看锁定档。
func TestLockedTierStable(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.RiderOnline(0, "r1", "A"), "on")
	mustOK(t, s.CreateOrder(1, "o1", "A"), "c1") // 锁定档0
	mustOK(t, s.RiderOffline(2, "r1"), "off")
	_, err := s.Evaluate(5, "A")
	mustOK(t, err, "up3")
	mustOK(t, s.RiderOnline(10, "r1", "A"), "on2")
	mustOK(t, s.DispatchOrder(11, "o1", "r1"), "dispatch")
	amt, err := s.CompleteOrder(12, "o1")
	mustOK(t, err, "complete")
	if amt != 0 {
		t.Fatalf("locked base tier: subsidy want 0 despite current tier3, got %d", amt)
	}
	if s.Snapshot().Orders["o1"].LockedTier != 0 {
		t.Fatalf("locked tier must remain 0")
	}
}

// TestSubsidyByLockedTier 高档创建的订单按锁定档补贴。
func TestSubsidyByLockedTier(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.RiderOnline(0, "r1", "A"), "on")
	mustOK(t, s.CreateOrder(1, "o1", "A"), "c1")
	mustOK(t, s.CreateOrder(1, "o2", "A"), "c2")
	_, err := s.Evaluate(5, "A")
	mustOK(t, err, "tier2") // ratio=2
	mustOK(t, s.CreateOrder(6, "o3", "A"), "c3 at tier2")
	mustOK(t, s.DispatchOrder(7, "o3", "r1"), "dispatch")
	amt, err := s.CompleteOrder(8, "o3")
	mustOK(t, err, "complete")
	if amt != 20 { // subsidies[2]=20
		t.Fatalf("tier2 locked subsidy want 20, got %d", amt)
	}
	total, _ := s.SubsidyTotal("r1")
	if total != 20 {
		t.Fatalf("total want 20, got %d", total)
	}
}

// TestLateEntryExemption 进入时刻恰等于创建时刻有补贴；晚于则豁免为0。
func TestLateEntryExemption(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	// 先拉爆供需升到档3，再在 t=1 创建订单。
	for _, o := range []OrderID{"g1", "g2", "g3"} {
		mustOK(t, s.CreateOrder(0, o, "A"), "gap order")
	}
	_, err := s.Evaluate(0, "A")
	mustOK(t, err, "up3") // cap=0 => 档3
	mustOK(t, s.CreateOrder(1, "o1", "A"), "c1")
	mustOK(t, s.CreateOrder(1, "o2", "A"), "c2")
	mustOK(t, s.RiderOnline(1, "r1", "A"), "on at creation time") // 进入时刻==创建时刻
	mustOK(t, s.DispatchOrder(5, "o1", "r1"), "d1")
	amt, _ := s.CompleteOrder(6, "o1")
	if amt != 40 {
		t.Fatalf("entry==create: want subsidy 40, got %d", amt)
	}
	mustOK(t, s.RiderOnline(7, "r2", "A"), "late rider") // 进入时刻 7 > 创建时刻 1
	mustOK(t, s.DispatchOrder(8, "o2", "r2"), "d2")
	amt, err = s.CompleteOrder(9, "o2")
	mustOK(t, err, "complete")
	if amt != 0 {
		t.Fatalf("late entry: want 0, got %d", amt)
	}
	entries, _ := s.SubsidyEntries("r2")
	if len(entries) != 1 || !entries[0].WaivedLate || entries[0].Tier != 3 {
		t.Fatalf("late entry ledger: %+v", entries)
	}
}

// TestMinEvalIntervalEquality 间隔取等合法；少一秒报过频。
func TestMinEvalIntervalEquality(t *testing.T) {
	s, _ := NewSystem(testCfg()) // interval=5
	mustOK(t, s.AddRegion("A"), "add")
	_, err := s.Evaluate(10, "A")
	mustOK(t, err, "first")
	_, err = s.Evaluate(14, "A")
	assertCode(t, err, ErrEvaluateTooFrequent, "interval 4")
	_, err = s.Evaluate(15, "A")
	mustOK(t, err, "interval 5 (equal)")
}

// TestClockRollbackAndRefusal 时钟回退与拒绝次序、拒绝不改状态。
func TestClockRollbackAndRefusal(t *testing.T) {
	s, _ := NewSystem(testCfg())
	mustOK(t, s.AddRegion("A"), "add")
	mustOK(t, s.RiderOnline(10, "r1", "A"), "on at 10")
	assertCode(t, s.RiderOnline(9, "r2", "A"), ErrClockRollback, "rollback")
	// 被拒绝操作不引入 r2。
	err := s.RiderOffline(10, "r2")
	assertCode(t, err, ErrRiderNotFound, "r2 must not exist")
	// 对象不存在错误分别可区分。
	assertCode(t, s.RiderOffline(11, "ghost"), ErrRiderNotFound, "no rider")
	assertCode(t, s.RiderOnline(11, "r3", "Z"), ErrRegionNotFound, "no region")
	assertCode(t, s.CancelOrder(11, "no"), ErrOrderNotFound, "no order")
	// 状态类错误。
	assertCode(t, s.RiderOnline(11, "r1", "A"), ErrRiderAlreadyOnline, "dup online")
	mustOK(t, s.RiderOffline(12, "r1"), "off")
	assertCode(t, s.RiderOffline(13, "r1"), ErrRiderAlreadyOffline, "dup offline")
	assertCode(t, s.RiderMove(14, "r1", "A"), ErrRiderAlreadyOffline, "move offline")
	// 移动到当前区域。
	mustOK(t, s.RiderOnline(15, "r1", "A"), "on")
	assertCode(t, s.RiderMove(16, "r1", "A"), ErrMoveNotNeeded, "same region")
}

// TestDispatchEligibilityOrder 派单资格报错次序：
// 不在线 → 不在本区 → 持单已满。
func TestDispatchEligibilityOrder(t *testing.T) {
	s, _ := NewSystem(testCfg()) // MaxHeld=2
	mustOK(t, s.AddRegion("A"), "A")
	mustOK(t, s.AddRegion("B"), "B")
	mustOK(t, s.RiderOnline(1, "busy", "A"), "busy online")
	mustOK(t, s.CreateOrder(2, "o1", "A"), "o1")
	mustOK(t, s.CreateOrder(2, "o2", "A"), "o2")
	mustOK(t, s.CreateOrder(2, "o3", "A"), "o3")
	mustOK(t, s.CreateOrder(2, "o4", "A"), "o4")
	mustOK(t, s.DispatchOrder(2, "o1", "busy"), "d1")
	mustOK(t, s.DispatchOrder(3, "o2", "busy"), "d2 full")
	mustOK(t, s.RiderOnline(4, "far", "B"), "far online")

	// 不在线骑手优先于区域/持单判定。
	assertCode(t, s.DispatchOrder(5, "o3", "off"), ErrRiderNotFound, "rider missing")
	mustOK(t, s.RiderOnline(5, "off", "A"), "off rider online")
	mustOK(t, s.RiderOffline(6, "off"), "off rider offline")
	assertCode(t, s.DispatchOrder(7, "o3", "off"), ErrRiderOffline, "offline first")
	// 在线但在 B 区：在本区判定先于持单已满。
	assertCode(t, s.DispatchOrder(8, "o3", "far"), ErrRiderWrongRegion, "wrong region")
	// 同区在线且持单已满。
	assertCode(t, s.DispatchOrder(9, "o3", "busy"), ErrRiderHoldFull, "full")
	// 被拒订单仍是待派。
	if p := s.Snapshot().Regions["A"].Pending; p != 2 {
		t.Fatalf("refused dispatch keeps pending=2, got %d", p)
	}
	// 订单状态类错误。
	mustOK(t, s.CancelOrder(10, "o3"), "cancel")
	assertCode(t, s.DispatchOrder(11, "o3", "far"), ErrOrderCancelled, "cancelled order")
	_, compErr := s.CompleteOrder(12, "o4")
	assertCode(t, compErr, ErrOrderNotDispatched, "complete pending")
}

// TestConfigValidation 非法构造参数。
func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{nil, 2, 2, []int64{0}, 5, nil},
		{[]float64{2, 1}, 2, 2, []int64{0, 1, 2}, 5, nil}, // 非递增
		{[]float64{1}, 0, 2, []int64{0, 1}, 5, nil},       // down<=0
		{[]float64{1}, 2, 0, []int64{0, 1}, 5, nil},       // held<=0
		{[]float64{1}, 2, 2, []int64{0, -1}, 5, nil},      // 负补贴
		{[]float64{1}, 2, 2, []int64{0}, 5, nil},          // 补贴长度不符
		{[]float64{1}, 2, 2, []int64{0, 1}, -1, nil},      // 负间隔
	}
	for i, c := range bad {
		if _, err := NewSystem(c); CodeOf(err) != ErrInvalidConfig {
			t.Fatalf("bad config #%d: want invalid_config, got %v", i, err)
		}
	}
}
