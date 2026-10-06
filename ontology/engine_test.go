package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const testPolicyID = "P"

func wantErr(t testing.TB, err error, code ErrCode) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("期望错误 %v，得到非引擎错误: %v", code, err)
	}
	if e.Code != code {
		t.Fatalf("期望错误 %v，得到 %v (%v)", code, e.Code, err)
	}
}

func wantNoErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

func wantPayouts(t testing.TB, got []int64, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("赔付明细数不符: 得到 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("赔付额不符: 得到 %v，期望 %v", got, want)
		}
	}
}

// 标准保单：起始日 100，年度长度 10，年度 k = [100+10k, 110+10k)。
func register(t testing.TB, e *Engine, family int64, members []MemberInput, items []ItemInput) {
	t.Helper()
	wantNoErr(t, e.RegisterPolicy(PolicyInput{
		ID:                testPolicyID,
		StartDay:          100,
		YearLength:        10,
		FamilyAnnualLimit: family,
		Members:           members,
		Items:             items,
	}))
}

func stdMembers(annual, lifetime int64) []MemberInput {
	return []MemberInput{{ID: "m1", AnnualLimit: annual, LifetimeLimit: lifetime}}
}

func stdItems(limits ...int64) []ItemInput {
	items := make([]ItemInput, len(limits))
	for i, l := range limits {
		items[i] = ItemInput{ID: fmt.Sprintf("i%d", i+1), AnnualLimit: l}
	}
	return items
}

func settle1(t testing.TB, e *Engine, claimID, memberID, itemID string, day, amount int64) []int64 {
	t.Helper()
	pay, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID:       claimID,
		MemberID: memberID,
		Lines:    []LineInput{{ItemID: itemID, Day: day, Amount: amount}},
	})
	wantNoErr(t, err)
	return pay
}

func snapshot(t testing.TB, e *Engine, memberID, itemID string, year int64) Snapshot {
	t.Helper()
	snap, err := e.Snapshot(testPolicyID, memberID, itemID, year)
	wantNoErr(t, err)
	return snap
}

// 发生日恰等于年度右端（110）时落入下一年度。
func TestYearRightEdgeFallsIntoNextYear(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(100, 10000), stdItems(1000))

	pay := settle1(t, e, "c1", "m1", "i1", 109, 100)
	wantPayouts(t, pay, 100) // 年度 0 个人年度限额用尽

	// 若 day 110 被错算进年度 0，个人年度剩余为 0，将赔付 0。
	pay = settle1(t, e, "c2", "m1", "i1", 110, 100)
	wantPayouts(t, pay, 100)

	if got := snapshot(t, e, "m1", "i1", 0).MemberAnnualRemaining; got != 0 {
		t.Fatalf("年度 0 个人年度剩余应为 0，得到 %d", got)
	}
	if got := snapshot(t, e, "m1", "i1", 1).MemberAnnualRemaining; got != 0 {
		t.Fatalf("年度 1 个人年度剩余应为 0，得到 %d", got)
	}
}

// 同一笔理赔的明细跨两个保单年度，分别受各自年度限额约束。
func TestClaimSpanningTwoYears(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(100, 10000), stdItems(10000))

	pay, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID:       "c1",
		MemberID: "m1",
		Lines: []LineInput{
			{ItemID: "i1", Day: 105, Amount: 150}, // 年度 0，限额 100
			{ItemID: "i1", Day: 115, Amount: 150}, // 年度 1，限额 100
		},
	})
	wantNoErr(t, err)
	wantPayouts(t, pay, 100, 100)

	if got := snapshot(t, e, "m1", "i1", 0).MemberAnnualRemaining; got != 0 {
		t.Fatalf("年度 0 剩余应为 0，得到 %d", got)
	}
	if got := snapshot(t, e, "m1", "i1", 1).MemberAnnualRemaining; got != 0 {
		t.Fatalf("年度 1 剩余应为 0，得到 %d", got)
	}
}

// 五个候选值中的每一个单独成为最小值时，赔付额由它决定。
func TestEachLayerCanBeSoleMinimum(t *testing.T) {
	cases := []struct {
		name                                         string
		amount, item, member, family, lifetime, want int64
	}{
		{"明细金额最小", 50, 1000, 1000, 1000, 1000, 50},
		{"项目年度限额最小", 100, 40, 1000, 1000, 1000, 40},
		{"个人年度限额最小", 100, 1000, 30, 1000, 1000, 30},
		{"家庭年度限额最小", 100, 1000, 1000, 20, 1000, 20},
		{"个人终身限额最小", 100, 1000, 1000, 1000, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine()
			register(t, e, tc.family, stdMembers(tc.member, tc.lifetime), stdItems(tc.item))
			pay := settle1(t, e, "c1", "m1", "i1", 100, tc.amount)
			wantPayouts(t, pay, tc.want)
		})
	}
}

// 同项目同年度的多条明细按顺序扣减，扣减对后续明细立即可见。
func TestSameItemSameYearSequentialDeduction(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(100))

	pay, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID:       "c1",
		MemberID: "m1",
		Lines: []LineInput{
			{ItemID: "i1", Day: 100, Amount: 60},
			{ItemID: "i1", Day: 101, Amount: 60}, // 项目年度仅剩 40
		},
	})
	wantNoErr(t, err)
	wantPayouts(t, pay, 60, 40)
}

// 明细无论输入顺序如何，都按（项目编号， 发生日）升序扣减；
// 返回的赔付额仍与输入明细一一对应。
func TestLinesSettledInSortedOrder(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(100, 1000))

	pay, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID:       "c1",
		MemberID: "m1",
		Lines: []LineInput{
			{ItemID: "i2", Day: 100, Amount: 50}, // 后处理：i2 排在 i1 之后
			{ItemID: "i1", Day: 100, Amount: 80}, // 先处理：赔 80
			{ItemID: "i1", Day: 100, Amount: 50}, // 再处理：i1 年度仅剩 20
		},
	})
	wantNoErr(t, err)
	wantPayouts(t, pay, 50, 80, 20)
}

// 终身限额恰好耗尽后成员转为已封顶，下一笔整笔报已封顶而非赔零。
func TestCappedAfterLifetimeExhausted(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 100), stdItems(1000))

	pay := settle1(t, e, "c1", "m1", "i1", 100, 100)
	wantPayouts(t, pay, 100)
	if !snapshot(t, e, "m1", "i1", 0).Capped {
		t.Fatal("终身限额耗尽后应为已封顶")
	}
	_, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID: "c2", MemberID: "m1",
		Lines: []LineInput{{ItemID: "i1", Day: 101, Amount: 10}},
	})
	wantErr(t, err, ErrCapped)
}

// 冲正使终身剩余恢复为正后，状态自动回到正常。
func TestReverseRestoresCappedStatus(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 100), stdItems(1000))

	settle1(t, e, "c1", "m1", "i1", 100, 100)
	if !snapshot(t, e, "m1", "i1", 0).Capped {
		t.Fatal("应为已封顶")
	}
	wantNoErr(t, e.ReverseClaim(testPolicyID, "c1"))
	snap := snapshot(t, e, "m1", "i1", 0)
	if snap.Capped || snap.LifetimeRemaining != 100 {
		t.Fatalf("冲正后状态应恢复正常且终身剩余 100，得到 %+v", snap)
	}
	pay := settle1(t, e, "c2", "m1", "i1", 100, 60)
	wantPayouts(t, pay, 60)
}

// 批改降低限额低于已用额后剩余额为零，且不追索已结算理赔。
func TestEndorseBelowUsedNoClawback(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(100, 10000), stdItems(10000))

	pay := settle1(t, e, "c1", "m1", "i1", 100, 100)
	wantPayouts(t, pay, 100)

	// 生效日等于最晚发生日，允许批改（取等不判追溯）。
	wantNoErr(t, e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 100, 60))
	if got := snapshot(t, e, "m1", "i1", 0).MemberAnnualRemaining; got != 0 {
		t.Fatalf("新限额低于已用额，剩余应为 0，得到 %d", got)
	}
	// 不追索：已结算的 100 不退回，终身已用保持 100。
	if got := snapshot(t, e, "m1", "i1", 0).LifetimeRemaining; got != 9900 {
		t.Fatalf("不追索，终身剩余应为 9900，得到 %d", got)
	}
	// 年度 0 内新理赔受新限额约束，赔付 0。
	pay = settle1(t, e, "c2", "m1", "i1", 101, 50)
	wantPayouts(t, pay, 0)
	// 批改对之后年度同样生效。
	pay = settle1(t, e, "c3", "m1", "i1", 110, 80)
	wantPayouts(t, pay, 60)
}

// 批改后冲正：恢复后的剩余额不得超过批改后的限额，超过部分不恢复。
func TestReverseAfterEndorseCappedByNewLimit(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(100, 10000), stdItems(10000))

	settle1(t, e, "c1", "m1", "i1", 100, 60)
	settle1(t, e, "c2", "m1", "i1", 101, 40) // 年度已用 100
	wantNoErr(t, e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 101, 70))

	wantNoErr(t, e.ReverseClaim(testPolicyID, "c2")) // 已用回到 60
	// 若按原限额恢复则剩余 40；但新限额 70 下剩余只能为 10。
	if got := snapshot(t, e, "m1", "i1", 0).MemberAnnualRemaining; got != 10 {
		t.Fatalf("恢复后剩余应为 10，得到 %d", got)
	}
	pay := settle1(t, e, "c3", "m1", "i1", 102, 50)
	wantPayouts(t, pay, 10)
}

// 追溯批改判定取等：生效日等于最晚发生日允许，早一天则拒绝。
func TestRetroactiveBoundaryEqual(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))

	settle1(t, e, "c1", "m1", "i1", 105, 10)
	wantNoErr(t, e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 105, 500))
	wantErr(t, e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 104, 500), ErrRetroactive)
	wantErr(t, e.Endorse(testPolicyID, EndorseItemAnnual, "i1", 100, 500), ErrRetroactive)
	wantErr(t, e.Endorse(testPolicyID, EndorseFamilyAnnual, "", 100, 500), ErrRetroactive)
	// 生效日早于承保起始日属于参数非法（次序高于追溯批改）。
	wantErr(t, e.Endorse(testPolicyID, EndorseFamilyAnnual, "", 99, 500), ErrInvalidParam)
}

// 同一对象同一生效日的两次批改以后一次为准。
func TestEndorseSameDayLaterWins(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))

	wantNoErr(t, e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 100, 500))
	wantNoErr(t, e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 100, 300))
	pay := settle1(t, e, "c1", "m1", "i1", 100, 400)
	wantPayouts(t, pay, 300)
}

// 非末笔冲正被拒绝且不留痕：状态不变、理赔号不释放。
func TestReverseNotLastLeavesNoTrace(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))

	settle1(t, e, "c1", "m1", "i1", 100, 30)
	settle1(t, e, "c2", "m1", "i1", 101, 20)

	wantErr(t, e.ReverseClaim(testPolicyID, "c1"), ErrNotLastClaim)
	if got := snapshot(t, e, "m1", "i1", 0).MemberAnnualRemaining; got != 950 {
		t.Fatalf("非末笔冲正不得改变剩余额，得到 %d", got)
	}
	// c1 未释放，重复使用仍报理赔已存在。
	_, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID: "c1", MemberID: "m1",
		Lines: []LineInput{{ItemID: "i1", Day: 100, Amount: 1}},
	})
	wantErr(t, err, ErrClaimExists)
	// 依次冲正末笔后，c1 成为末笔，可以冲正。
	wantNoErr(t, e.ReverseClaim(testPolicyID, "c2"))
	wantNoErr(t, e.ReverseClaim(testPolicyID, "c1"))
	if got := snapshot(t, e, "m1", "i1", 0).MemberAnnualRemaining; got != 1000 {
		t.Fatalf("全部冲正后剩余应为 1000，得到 %d", got)
	}
}

// 冲正后理赔号释放，可重新使用。
func TestClaimIDReleasedAfterReverse(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))

	settle1(t, e, "c1", "m1", "i1", 100, 30)
	wantNoErr(t, e.ReverseClaim(testPolicyID, "c1"))
	pay := settle1(t, e, "c1", "m1", "i1", 100, 50)
	wantPayouts(t, pay, 50)
}

// 一笔理赔中任一明细被拒，整笔拒绝且全部剩余额不变。
func TestClaimAllOrNothing(t *testing.T) {
	e := NewEngine()
	register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))

	_, err := e.SettleClaim(testPolicyID, ClaimInput{
		ID:       "c1",
		MemberID: "m1",
		Lines: []LineInput{
			{ItemID: "i1", Day: 100, Amount: 30},
			{ItemID: "i1", Day: 50, Amount: 20}, // 早于承保起始日
		},
	})
	wantErr(t, err, ErrDayNotCovered)
	snap := snapshot(t, e, "m1", "i1", 0)
	if snap.MemberAnnualRemaining != 1000 || snap.ItemAnnualRemaining != 1000 ||
		snap.FamilyAnnualRemaining != 10000 || snap.LifetimeRemaining != 10000 {
		t.Fatalf("整笔拒绝不得改变任何剩余额，得到 %+v", snap)
	}
}

// 多成员并发争抢家庭共享年度限额：总额恰好分完，结果等价于某个串行顺序。
func TestConcurrentFamilyContention(t *testing.T) {
	e := NewEngine()
	members := make([]MemberInput, 20)
	for i := range members {
		members[i] = MemberInput{ID: fmt.Sprintf("m%d", i), AnnualLimit: 10000, LifetimeLimit: 100000}
	}
	register(t, e, 1000, members, stdItems(10000))

	payouts := make([]int64, 20)
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			pay, err := e.SettleClaim(testPolicyID, ClaimInput{
				ID:       fmt.Sprintf("c%d", g),
				MemberID: fmt.Sprintf("m%d", g),
				Lines:    []LineInput{{ItemID: "i1", Day: 100, Amount: 100}},
			})
			if err != nil {
				t.Errorf("并发结算失败: %v", err)
				return
			}
			payouts[g] = pay[0]
		}(g)
	}
	wg.Wait()

	var sum int64
	full := 0
	for _, p := range payouts {
		if p != 0 && p != 100 {
			t.Fatalf("赔付额 %d 非 0 即 100（串行语义被破坏）", p)
		}
		if p == 100 {
			full++
		}
		sum += p
	}
	if sum != 1000 || full != 10 {
		t.Fatalf("家庭限额 1000 应恰好分完（10 笔足额），得到总额 %d、足额 %d 笔", sum, full)
	}
	if got := snapshot(t, e, "m0", "i1", 0).FamilyAnnualRemaining; got != 0 {
		t.Fatalf("家庭年度剩余应为 0，得到 %d", got)
	}
}

// 拒绝次序逐对验证：两类错误同时成立时，只报次序最前者。
func TestRejectOrderPairs(t *testing.T) {
	newCappedEngine := func(t *testing.T) *Engine {
		e := NewEngine()
		register(t, e, 10000, stdMembers(1000, 10), stdItems(1000))
		settle1(t, e, "c1", "m1", "i1", 100, 10) // 终身限额 10 耗尽，m1 已封顶
		return e
	}

	t.Run("参数非法优先于保单不存在", func(t *testing.T) {
		e := NewEngine()
		_, err := e.SettleClaim("NOPE", ClaimInput{ID: "", MemberID: "m1",
			Lines: []LineInput{{ItemID: "i1", Day: 100, Amount: 1}}})
		wantErr(t, err, ErrInvalidParam)
	})
	t.Run("保单不存在优先于成员不存在", func(t *testing.T) {
		e := NewEngine()
		_, err := e.SettleClaim("NOPE", ClaimInput{ID: "c1", MemberID: "nobody",
			Lines: []LineInput{{ItemID: "i1", Day: 100, Amount: 1}}})
		wantErr(t, err, ErrPolicyNotFound)
	})
	t.Run("成员不存在优先于项目不存在", func(t *testing.T) {
		e := NewEngine()
		register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))
		_, err := e.SettleClaim(testPolicyID, ClaimInput{ID: "c1", MemberID: "nobody",
			Lines: []LineInput{{ItemID: "nothing", Day: 100, Amount: 1}}})
		wantErr(t, err, ErrMemberNotFound)
	})
	t.Run("项目不存在优先于理赔已存在", func(t *testing.T) {
		e := NewEngine()
		register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))
		settle1(t, e, "c1", "m1", "i1", 100, 10)
		_, err := e.SettleClaim(testPolicyID, ClaimInput{ID: "c1", MemberID: "m1",
			Lines: []LineInput{{ItemID: "nothing", Day: 100, Amount: 1}}})
		wantErr(t, err, ErrItemNotFound)
	})
	t.Run("成员不存在优先于理赔已存在", func(t *testing.T) {
		e := NewEngine()
		register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))
		settle1(t, e, "c1", "m1", "i1", 100, 10)
		_, err := e.SettleClaim(testPolicyID, ClaimInput{ID: "c1", MemberID: "nobody",
			Lines: []LineInput{{ItemID: "i1", Day: 100, Amount: 1}}})
		wantErr(t, err, ErrMemberNotFound)
	})
	t.Run("理赔已存在优先于已封顶", func(t *testing.T) {
		e := newCappedEngine(t)
		_, err := e.SettleClaim(testPolicyID, ClaimInput{ID: "c1", MemberID: "m1",
			Lines: []LineInput{{ItemID: "i1", Day: 100, Amount: 1}}})
		wantErr(t, err, ErrClaimExists)
	})
	t.Run("已封顶优先于发生日未承保", func(t *testing.T) {
		e := newCappedEngine(t)
		_, err := e.SettleClaim(testPolicyID, ClaimInput{ID: "c2", MemberID: "m1",
			Lines: []LineInput{{ItemID: "i1", Day: 50, Amount: 1}}})
		wantErr(t, err, ErrCapped)
	})
	t.Run("参数非法优先于成员重复", func(t *testing.T) {
		e := NewEngine()
		err := e.RegisterPolicy(PolicyInput{
			ID: testPolicyID, StartDay: 100, YearLength: 10, FamilyAnnualLimit: 100,
			Members: []MemberInput{
				{ID: "m1", AnnualLimit: -1, LifetimeLimit: 1},
				{ID: "m1", AnnualLimit: 1, LifetimeLimit: 1},
			},
			Items: stdItems(100),
		})
		wantErr(t, err, ErrInvalidParam)
	})
	t.Run("成员重复优先于项目重复", func(t *testing.T) {
		e := NewEngine()
		err := e.RegisterPolicy(PolicyInput{
			ID: testPolicyID, StartDay: 100, YearLength: 10, FamilyAnnualLimit: 100,
			Members: []MemberInput{
				{ID: "m1", AnnualLimit: 1, LifetimeLimit: 1},
				{ID: "m1", AnnualLimit: 1, LifetimeLimit: 1},
			},
			Items: []ItemInput{{ID: "i1", AnnualLimit: 1}, {ID: "i1", AnnualLimit: 1}},
		})
		wantErr(t, err, ErrMemberDuplicate)
	})
	t.Run("理赔不存在优先于非末笔", func(t *testing.T) {
		e := NewEngine()
		register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))
		settle1(t, e, "c1", "m1", "i1", 100, 10)
		settle1(t, e, "c2", "m1", "i1", 100, 10)
		wantErr(t, e.ReverseClaim(testPolicyID, "ghost"), ErrClaimNotFound)
		wantErr(t, e.ReverseClaim(testPolicyID, "c1"), ErrNotLastClaim)
	})
	t.Run("成员不存在优先于追溯批改", func(t *testing.T) {
		e := NewEngine()
		register(t, e, 10000, stdMembers(1000, 10000), stdItems(1000))
		settle1(t, e, "c1", "m1", "i1", 105, 10)
		err := e.Endorse(testPolicyID, EndorseMemberAnnual, "nobody", 100, 500)
		wantErr(t, err, ErrMemberNotFound)
	})
}

// 重复登记分别报成员重复与项目重复。
func TestDuplicateRegistration(t *testing.T) {
	e := NewEngine()
	err := e.RegisterPolicy(PolicyInput{
		ID: testPolicyID, StartDay: 100, YearLength: 10, FamilyAnnualLimit: 100,
		Members: []MemberInput{
			{ID: "m1", AnnualLimit: 1, LifetimeLimit: 1},
			{ID: "m1", AnnualLimit: 2, LifetimeLimit: 2},
		},
		Items: stdItems(100),
	})
	wantErr(t, err, ErrMemberDuplicate)

	err = e.RegisterPolicy(PolicyInput{
		ID: testPolicyID, StartDay: 100, YearLength: 10, FamilyAnnualLimit: 100,
		Members: stdMembers(100, 1000),
		Items:   []ItemInput{{ID: "i1", AnnualLimit: 1}, {ID: "i1", AnnualLimit: 2}},
	})
	wantErr(t, err, ErrItemDuplicate)
}

// 相同操作序列重放得到完全相同的赔付额。
func TestReplayDeterministic(t *testing.T) {
	run := func() [][]int64 {
		e := NewEngine()
		register(t, e, 150, stdMembers(100, 120), stdItems(80))
		var out [][]int64
		pay, _ := e.SettleClaim(testPolicyID, ClaimInput{ID: "c1", MemberID: "m1",
			Lines: []LineInput{{ItemID: "i1", Day: 100, Amount: 90}}})
		out = append(out, pay)
		_ = e.Endorse(testPolicyID, EndorseMemberAnnual, "m1", 100, 200)
		pay, _ = e.SettleClaim(testPolicyID, ClaimInput{ID: "c2", MemberID: "m1",
			Lines: []LineInput{{ItemID: "i1", Day: 101, Amount: 100}}})
		out = append(out, pay)
		_ = e.ReverseClaim(testPolicyID, "c2")
		pay, _ = e.SettleClaim(testPolicyID, ClaimInput{ID: "c3", MemberID: "m1",
			Lines: []LineInput{{ItemID: "i1", Day: 102, Amount: 100}}})
		out = append(out, pay)
		return out
	}
	first, second := run(), run()
	for i := range first {
		wantPayouts(t, second[i], first[i]...)
	}
}

// 性能证明：结算一笔理赔的工作量计数只随本笔明细数增长，
// 与历史理赔笔数、成员总数、项目总数无关。
func TestSettleCostIndependentOfHistory(t *testing.T) {
	e := NewEngine()
	members := make([]MemberInput, 2000)
	for i := range members {
		members[i] = MemberInput{ID: fmt.Sprintf("m%d", i), AnnualLimit: 1 << 50, LifetimeLimit: 1 << 60}
	}
	items := make([]ItemInput, 2000)
	for i := range items {
		items[i] = ItemInput{ID: fmt.Sprintf("i%d", i), AnnualLimit: 1 << 50}
	}
	register(t, e, 1<<60, members, items)

	oneLine := func(id string) ClaimInput {
		return ClaimInput{ID: id, MemberID: "m0",
			Lines: []LineInput{{ItemID: "i0", Day: 100, Amount: 1}}}
	}
	before := e.WorkUnits(testPolicyID)
	_, err := e.SettleClaim(testPolicyID, oneLine("warm"))
	wantNoErr(t, err)
	costEmpty := e.WorkUnits(testPolicyID) - before

	// 沉淀 5000 笔历史理赔（分布在不同成员、项目与年度）。
	for n := 0; n < 5000; n++ {
		_, err := e.SettleClaim(testPolicyID, ClaimInput{
			ID:       fmt.Sprintf("h%d", n),
			MemberID: fmt.Sprintf("m%d", n%2000),
			Lines: []LineInput{{
				ItemID: fmt.Sprintf("i%d", n%2000),
				Day:    100 + int64(n%50),
				Amount: 1,
			}},
		})
		wantNoErr(t, err)
	}

	before = e.WorkUnits(testPolicyID)
	_, err = e.SettleClaim(testPolicyID, oneLine("probe"))
	wantNoErr(t, err)
	costAfter := e.WorkUnits(testPolicyID) - before

	if costEmpty == 0 || costAfter != costEmpty {
		t.Fatalf("单笔理赔工作量应恒定：空账 %d，5000 笔历史后 %d", costEmpty, costAfter)
	}

	// 工作量与明细条数成正比。
	before = e.WorkUnits(testPolicyID)
	lines := make([]LineInput, 7)
	for i := range lines {
		lines[i] = LineInput{ItemID: fmt.Sprintf("i%d", i), Day: 100, Amount: 1}
	}
	_, err = e.SettleClaim(testPolicyID, ClaimInput{ID: "multi", MemberID: "m1", Lines: lines})
	wantNoErr(t, err)
	if got, want := e.WorkUnits(testPolicyID)-before, 7*costEmpty; got != want {
		t.Fatalf("7 条明细工作量应为 %d，得到 %d", want, got)
	}
}

// 稳态基准：预先沉淀大量历史后结算单笔理赔。
func BenchmarkSettleSteadyState(b *testing.B) {
	e := NewEngine()
	register(b, e, 1<<60, stdMembers(1<<50, 1<<60), stdItems(1<<50))
	for n := 0; n < 10000; n++ {
		if _, err := e.SettleClaim(testPolicyID, ClaimInput{
			ID:       fmt.Sprintf("h%d", n),
			MemberID: "m1",
			Lines:    []LineInput{{ItemID: "i1", Day: 100 + int64(n), Amount: 1}},
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := e.SettleClaim(testPolicyID, ClaimInput{
			ID:       fmt.Sprintf("b%d", n),
			MemberID: "m1",
			Lines:    []LineInput{{ItemID: "i1", Day: 20000 + int64(n), Amount: 1}},
		}); err != nil {
			b.Fatal(err)
		}
	}
}
