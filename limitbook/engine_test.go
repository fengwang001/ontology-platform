package limitbook

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功, 得到错误: %v", err)
	}
}

func mustKind(t *testing.T, err error, k Kind) {
	t.Helper()
	if !IsKind(err, k) {
		t.Fatalf("期望错误「%s」, 得到: %v", k, err)
	}
}

// newFixture 建立标准保单：start=0, yearLen=365, 家庭年度限额 fam。
func newFixture(t *testing.T, fam int64) *Engine {
	t.Helper()
	e := NewEngine()
	mustOK(t, e.RegisterPolicy("P", 0, 365, fam))
	return e
}

func addMember(t *testing.T, e *Engine, id string, annual, lifetime int64) {
	t.Helper()
	mustOK(t, e.RegisterMember("P", id, annual, lifetime))
}

func addItem(t *testing.T, e *Engine, id string, annual int64) {
	t.Helper()
	mustOK(t, e.RegisterItem("P", id, annual))
}

func settle(t *testing.T, e *Engine, c Claim) *Settlement {
	t.Helper()
	s, err := e.Settle("P", c)
	mustOK(t, err)
	return s
}

func mustPaid(t *testing.T, s *Settlement, want ...int64) {
	t.Helper()
	if !reflect.DeepEqual(s.Paid, want) {
		t.Fatalf("赔付额不符: 得到 %v, 期望 %v", s.Paid, want)
	}
}

// 发生日恰等于年度右端时落入下一年度（左闭右开）。
func TestYearBoundaryRightEdge(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy("P", 100, 365, 150))
	addMember(t, e, "M1", 10000, 100000)
	addItem(t, e, "I1", 10000)

	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 464, 150}}})
	mustPaid(t, s, 150)
	s = settle(t, e, Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 465, 150}}})
	mustPaid(t, s, 150)
	s = settle(t, e, Claim{ID: "C", MemberID: "M1", Details: []Detail{{"I1", 464, 1}}})
	mustPaid(t, s, 0)
	_, err := e.Settle("P", Claim{ID: "D", MemberID: "M1", Details: []Detail{{"I1", 99, 1}}})
	mustKind(t, err, ErrDayNotCovered)
}

// 同一笔理赔内跨两个保单年度的明细分别受各自年度限额约束。
func TestClaimSpanningTwoYears(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100, 100000)
	addItem(t, e, "I1", 100000)

	s := settle(t, e, Claim{ID: "C1", MemberID: "M1", Details: []Detail{
		{"I1", 364, 100},
		{"I1", 365, 100},
	}})
	mustPaid(t, s, 100, 100)

	s = settle(t, e, Claim{ID: "C2", MemberID: "M1", Details: []Detail{{"I1", 364, 50}}})
	mustPaid(t, s, 0)
	s = settle(t, e, Claim{ID: "C3", MemberID: "M1", Details: []Detail{{"I1", 365, 50}}})
	mustPaid(t, s, 0)
}

// 五层（明细金额、项目年度、个人年度、家庭年度、个人终身）各自单独成为最小值。
func TestEachLayerCanBeTheMinimum(t *testing.T) {
	cases := []struct {
		name                             string
		amount                           int64
		itemLim, memLim, famLim, lifeLim int64
		want                             int64
	}{
		{"明细金额最小", 50, 1000, 1000, 1000, 1000, 50},
		{"项目年度限额最小", 500, 40, 1000, 1000, 1000, 40},
		{"个人年度限额最小", 500, 1000, 30, 1000, 1000, 30},
		{"家庭年度限额最小", 500, 1000, 1000, 20, 1000, 20},
		{"个人终身限额最小", 500, 1000, 1000, 1000, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixture(t, tc.famLim)
			addMember(t, e, "M1", tc.memLim, tc.lifeLim)
			addItem(t, e, "I1", tc.itemLim)
			s := settle(t, e, Claim{ID: "C1", MemberID: "M1", Details: []Detail{{"I1", 10, tc.amount}}})
			mustPaid(t, s, tc.want)
		})
	}
}

// 同项目同年度多条明细按次序先后扣减，逐笔可见。
func TestSameItemSequentialDeduction(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100000, 100000)
	addItem(t, e, "I1", 100)

	s := settle(t, e, Claim{ID: "C1", MemberID: "M1", Details: []Detail{
		{"I1", 10, 60},
		{"I1", 20, 60},
		{"I1", 30, 60},
	}})
	mustPaid(t, s, 60, 40, 0)
}

// 明细按项目编号升序扣减，与输入顺序无关；Paid 按输入顺序回填。
func TestDetailSortOrder(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100, 100000)
	addItem(t, e, "I1", 100000)
	addItem(t, e, "I2", 100000)

	s := settle(t, e, Claim{ID: "C1", MemberID: "M1", Details: []Detail{
		{"I2", 10, 80},
		{"I1", 10, 80},
	}})
	mustPaid(t, s, 20, 80)
}

// 终身限额恰好耗尽后下一笔整笔报已封顶；冲正恢复后状态自动回到正常。
func TestLifetimeCappedAndRecover(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy("P", 100, 365, 100000))
	addMember(t, e, "M1", 100000, 100)
	addItem(t, e, "I1", 100000)

	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 200, 100}}})
	mustPaid(t, s, 100)

	_, err := e.Settle("P", Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 200, 1}}})
	mustKind(t, err, ErrCapped)
	// 已封顶优先于发生日未承保。
	_, err = e.Settle("P", Claim{ID: "B2", MemberID: "M1", Details: []Detail{{"I1", 50, 1}}})
	mustKind(t, err, ErrCapped)

	mustOK(t, e.Reverse("P", "A"))
	s = settle(t, e, Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 200, 30}}})
	mustPaid(t, s, 30)
}

// 批改降低限额后该年度剩余额视为零，且不追索已结算理赔。
func TestEndorseLowerToZeroNoClawback(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100, 100000)
	addItem(t, e, "I1", 100000)

	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 10, 80}}})
	mustPaid(t, s, 80)

	mustOK(t, e.EndorseMemberAnnual("P", "M1", 20, 50))

	s = settle(t, e, Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 30, 50}}})
	mustPaid(t, s, 0)
	// 新限额对之后年度生效。
	s = settle(t, e, Claim{ID: "C", MemberID: "M1", Details: []Detail{{"I1", 400, 50}}})
	mustPaid(t, s, 50)
	s = settle(t, e, Claim{ID: "D", MemberID: "M1", Details: []Detail{{"I1", 401, 1}}})
	mustPaid(t, s, 0)
	// 不追索：冲正 A 后按新限额 50 恢复，而不是旧限额 100。
	mustOK(t, e.Reverse("P", "D"))
	mustOK(t, e.Reverse("P", "C"))
	mustOK(t, e.Reverse("P", "B"))
	mustOK(t, e.Reverse("P", "A"))
	s = settle(t, e, Claim{ID: "E", MemberID: "M1", Details: []Detail{{"I1", 40, 100}}})
	mustPaid(t, s, 50)
}

// 批改后冲正，恢复后的剩余额不超过批改后的限额。
func TestReverseCappedAtEndorsedLimit(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100, 100000)
	addItem(t, e, "I1", 100000)

	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 10, 100}}})
	mustPaid(t, s, 100)
	mustOK(t, e.EndorseMemberAnnual("P", "M1", 20, 60))
	mustOK(t, e.Reverse("P", "A"))
	s = settle(t, e, Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 30, 100}}})
	mustPaid(t, s, 60)
}

// 同一对象同一生效日的批改以后一次为准；批改不影响更早年度。
func TestEndorseOverwriteAndNoBackdate(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100, 100000)
	addItem(t, e, "I1", 100000)

	mustOK(t, e.EndorseMemberAnnual("P", "M1", 10, 50))
	mustOK(t, e.EndorseMemberAnnual("P", "M1", 10, 70))
	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 20, 100}}})
	mustPaid(t, s, 70)

	// 生效日在年度1的批改不改变年度0的限额。
	e2 := newFixture(t, 100000)
	addMember(t, e2, "M1", 100, 100000)
	addItem(t, e2, "I1", 100000)
	mustOK(t, e2.EndorseMemberAnnual("P", "M1", 400, 30))
	s = settle(t, e2, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 10, 100}}})
	mustPaid(t, s, 100)
	s = settle(t, e2, Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 400, 100}}})
	mustPaid(t, s, 30)
}

// 追溯批改判定取等：生效日等于最晚发生日允许，早于则拒绝；冲正后限制解除。
func TestRetroactiveBoundary(t *testing.T) {
	e := newFixture(t, 100000)
	addMember(t, e, "M1", 100000, 100000)
	addItem(t, e, "I1", 100000)

	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 700, 10}}})
	mustPaid(t, s, 10)

	mustOK(t, e.EndorseFamilyAnnual("P", 700, 500))
	mustKind(t, e.EndorseFamilyAnnual("P", 699, 500), ErrRetroactive)
	mustOK(t, e.EndorseMemberAnnual("P", "M1", 700, 500))
	mustKind(t, e.EndorseItemAnnual("P", "I1", 0, 500), ErrRetroactive)

	mustOK(t, e.Reverse("P", "A"))
	mustOK(t, e.EndorseFamilyAnnual("P", 5, 800))
}

// 非末笔冲正被拒绝且不留痕；末笔冲正后理赔号释放可复用。
func TestReverseNotLastNoTrace(t *testing.T) {
	e := newFixture(t, 1000)
	addMember(t, e, "M1", 1000, 100000)
	addItem(t, e, "I1", 1000)

	s := settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 10, 100}}})
	mustPaid(t, s, 100)
	s = settle(t, e, Claim{ID: "B", MemberID: "M1", Details: []Detail{{"I1", 20, 100}}})
	mustPaid(t, s, 100)

	mustKind(t, e.Reverse("P", "A"), ErrNotLast)
	// 不留痕：家庭剩余额不变。
	s = settle(t, e, Claim{ID: "C", MemberID: "M1", Details: []Detail{{"I1", 30, 800}}})
	mustPaid(t, s, 800)

	mustOK(t, e.Reverse("P", "C"))
	mustOK(t, e.Reverse("P", "B"))
	mustOK(t, e.Reverse("P", "A"))
	mustKind(t, e.Reverse("P", "A"), ErrClaimNotFound)
	// 理赔号释放后可复用。
	s = settle(t, e, Claim{ID: "A", MemberID: "M1", Details: []Detail{{"I1", 40, 50}}})
	mustPaid(t, s, 50)
}

// 多成员并发争抢家庭共享额度：结果等价于某个串行顺序，
// 家庭已用不超过限额，且任一串行顺序下本例的赔付多重集相同。
func TestConcurrentFamilyContention(t *testing.T) {
	e := newFixture(t, 1050)
	for i := 0; i < 8; i++ {
		addMember(t, e, fmt.Sprintf("M%d", i), 100000, 1000000)
	}
	addItem(t, e, "I1", 100000)

	const claims = 40
	paids := make([]int64, claims)
	var wg sync.WaitGroup
	for i := 0; i < claims; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := e.Settle("P", Claim{
				ID:       fmt.Sprintf("C%d", i),
				MemberID: fmt.Sprintf("M%d", i%8),
				Details:  []Detail{{"I1", 10, 100}},
			})
			if err != nil {
				t.Errorf("理赔 C%d 应被受理: %v", i, err)
				return
			}
			paids[i] = s.Total
		}(i)
	}
	wg.Wait()

	var total int64
	var full, partial, zero int
	for _, p := range paids {
		total += p
		switch p {
		case 100:
			full++
		case 0:
			zero++
		default:
			partial++
		}
	}
	if total != 1050 || full != 10 || partial != 1 || zero != 29 {
		t.Fatalf("并发争抢结果不等于串行等价结果: total=%d full=%d partial=%d zero=%d", total, full, partial, zero)
	}
	p := e.policies["P"]
	p.mu.Lock()
	used := p.familyUsed[0]
	p.mu.Unlock()
	if used != 1050 {
		t.Fatalf("家庭已用应为 1050, 得到 %d", used)
	}
}

// 拒绝次序逐对验证：同一操作触发多个错误时只报次序最前者。
func TestErrorPrecedence(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy("P", 100, 365, 1000))
	addMember(t, e, "M1", 1000, 100000)
	addMember(t, e, "M2", 1000, 5) // 终身限额 5，一笔即封顶
	addItem(t, e, "I1", 1000)

	// 参数非法 > 保单不存在
	_, err := e.Settle("NOPE", Claim{ID: "X", MemberID: "M1", Details: []Detail{{"I1", 200, 0}}})
	mustKind(t, err, ErrInvalidParam)
	// 保单不存在 > 成员不存在
	_, err = e.Settle("NOPE", Claim{ID: "X", MemberID: "GHOST", Details: []Detail{{"I1", 200, 1}}})
	mustKind(t, err, ErrPolicyNotFound)
	// 保单不存在 > 成员重复
	mustKind(t, e.RegisterMember("NOPE", "M1", 1, 1), ErrPolicyNotFound)
	// 保单不存在 > 项目重复
	mustKind(t, e.RegisterItem("NOPE", "I1", 1), ErrPolicyNotFound)
	// 成员不存在 > 项目不存在
	_, err = e.Settle("P", Claim{ID: "X", MemberID: "GHOST", Details: []Detail{{"GHOST", 200, 1}}})
	mustKind(t, err, ErrMemberNotFound)

	// 制造一笔已受理理赔 C1。
	s := settle(t, e, Claim{ID: "C1", MemberID: "M1", Details: []Detail{{"I1", 200, 10}}})
	mustPaid(t, s, 10)
	// 项目不存在 > 理赔已存在
	_, err = e.Settle("P", Claim{ID: "C1", MemberID: "M1", Details: []Detail{{"GHOST", 200, 1}}})
	mustKind(t, err, ErrItemNotFound)
	// 成员不存在 > 理赔已存在
	_, err = e.Settle("P", Claim{ID: "C1", MemberID: "GHOST", Details: []Detail{{"I1", 200, 1}}})
	mustKind(t, err, ErrMemberNotFound)

	// 让 M2 封顶。
	s = settle(t, e, Claim{ID: "D1", MemberID: "M2", Details: []Detail{{"I1", 200, 5}}})
	mustPaid(t, s, 5)
	// 理赔已存在 > 已封顶
	_, err = e.Settle("P", Claim{ID: "D1", MemberID: "M2", Details: []Detail{{"I1", 200, 1}}})
	mustKind(t, err, ErrClaimExists)
	// 项目不存在 > 已封顶
	_, err = e.Settle("P", Claim{ID: "D2", MemberID: "M2", Details: []Detail{{"GHOST", 200, 1}}})
	mustKind(t, err, ErrItemNotFound)
	// 已封顶 > 发生日未承保
	_, err = e.Settle("P", Claim{ID: "D2", MemberID: "M2", Details: []Detail{{"I1", 50, 1}}})
	mustKind(t, err, ErrCapped)
	// 发生日未承保（未封顶成员）
	_, err = e.Settle("P", Claim{ID: "E1", MemberID: "M1", Details: []Detail{{"I1", 50, 1}}})
	mustKind(t, err, ErrDayNotCovered)

	// 理赔不存在 > 非末笔
	mustKind(t, e.Reverse("P", "NOPE"), ErrClaimNotFound)
	s = settle(t, e, Claim{ID: "F1", MemberID: "M1", Details: []Detail{{"I1", 300, 1}}})
	mustPaid(t, s, 1)
	mustKind(t, e.Reverse("P", "C1"), ErrNotLast)

	// 成员不存在 > 追溯批改
	mustKind(t, e.EndorseMemberAnnual("P", "GHOST", 100, 1), ErrMemberNotFound)
	mustKind(t, e.EndorseMemberAnnual("P", "M1", 100, 1), ErrRetroactive)
	// 项目不存在 > 追溯批改
	mustKind(t, e.EndorseItemAnnual("P", "GHOST", 100, 1), ErrItemNotFound)
	// 参数非法 > 成员不存在（批改）
	mustKind(t, e.EndorseMemberAnnual("P", "GHOST", -1, 1), ErrInvalidParam)
	// 参数非法 > 保单不存在（批改）
	mustKind(t, e.EndorseFamilyAnnual("NOPE", -1, 1), ErrInvalidParam)
}

// 一笔理赔中任一明细被拒即整笔拒绝，不改动任何剩余额与理赔记录。
func TestAllOrNothing(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy("P", 100, 365, 100))
	addMember(t, e, "M1", 100, 100000)
	addItem(t, e, "I1", 100)

	_, err := e.Settle("P", Claim{ID: "C1", MemberID: "M1", Details: []Detail{
		{"I1", 200, 60},
		{"I1", 50, 60}, // 发生日未承保
	}})
	mustKind(t, err, ErrDayNotCovered)
	_, err = e.Settle("P", Claim{ID: "C2", MemberID: "M1", Details: []Detail{
		{"I1", 200, 30},
		{"GHOST", 200, 30}, // 项目不存在
	}})
	mustKind(t, err, ErrItemNotFound)

	// 若上面两笔有任何扣减留痕，这里不可能全额赔付 100。
	s := settle(t, e, Claim{ID: "C3", MemberID: "M1", Details: []Detail{{"I1", 200, 100}}})
	mustPaid(t, s, 100)
	// 被拒的理赔号未留痕，可复用。
	s = settle(t, e, Claim{ID: "C1", MemberID: "M1", Details: []Detail{{"I1", 500, 100}}})
	mustPaid(t, s, 100)
}

// 登记类错误：成员重复、项目重复、保单重复、非法参数。
func TestRegistrationErrors(t *testing.T) {
	e := newFixture(t, 100)
	addMember(t, e, "M1", 1, 1)
	addItem(t, e, "I1", 1)

	mustKind(t, e.RegisterMember("P", "M1", 1, 1), ErrMemberDuplicate)
	mustKind(t, e.RegisterItem("P", "I1", 1), ErrItemDuplicate)
	mustKind(t, e.RegisterPolicy("P", 0, 365, 1), ErrInvalidParam)
	mustKind(t, e.RegisterPolicy("", 0, 365, 1), ErrInvalidParam)
	mustKind(t, e.RegisterPolicy("P2", -1, 365, 1), ErrInvalidParam)
	mustKind(t, e.RegisterPolicy("P2", 0, 0, 1), ErrInvalidParam)
	mustKind(t, e.RegisterPolicy("P2", 0, 365, -1), ErrInvalidParam)
	mustKind(t, e.RegisterMember("P", "", 1, 1), ErrInvalidParam)
	mustKind(t, e.RegisterMember("P", "M2", -1, 1), ErrInvalidParam)
	mustKind(t, e.RegisterItem("P", "I2", -1), ErrInvalidParam)
	mustKind(t, e.RegisterMember("NOPE", "M2", 1, 1), ErrPolicyNotFound)
	mustKind(t, e.RegisterItem("NOPE", "I2", 1), ErrPolicyNotFound)
}

// 相同操作序列重放得到完全相同的赔付额。
func TestReplayDeterminism(t *testing.T) {
	script := func(e *Engine) []*Settlement {
		var out []*Settlement
		mustOK(t, e.RegisterPolicy("P", 0, 365, 500))
		mustOK(t, e.RegisterMember("P", "M1", 300, 700))
		mustOK(t, e.RegisterMember("P", "M2", 300, 700))
		mustOK(t, e.RegisterItem("P", "I1", 200))
		mustOK(t, e.RegisterItem("P", "I2", 150))
		s, err := e.Settle("P", Claim{ID: "C1", MemberID: "M1", Details: []Detail{{"I1", 364, 120}, {"I2", 365, 90}, {"I1", 365, 80}}})
		mustOK(t, err)
		out = append(out, s)
		s, err = e.Settle("P", Claim{ID: "C2", MemberID: "M2", Details: []Detail{{"I1", 10, 200}}})
		mustOK(t, err)
		out = append(out, s)
		mustOK(t, e.EndorseFamilyAnnual("P", 400, 250))
		mustOK(t, e.Reverse("P", "C2"))
		s, err = e.Settle("P", Claim{ID: "C3", MemberID: "M2", Details: []Detail{{"I2", 400, 300}}})
		mustOK(t, err)
		out = append(out, s)
		return out
	}
	first := script(NewEngine())
	second := script(NewEngine())
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n第一次 %v\n第二次 %v", first, second)
	}
}

// 性能不变量：结算一笔理赔的账簿访问次数不随历史理赔笔数、
// 成员总数或项目总数增长，只与本笔明细数有关。
func TestSettleCostIndependentOfHistory(t *testing.T) {
	const huge = int64(1) << 60
	build := func(history, extraMembers, extraItems int) (*Engine, *Policy) {
		e := NewEngine()
		mustOK(t, e.RegisterPolicy("P", 0, 1<<30, huge))
		mustOK(t, e.RegisterMember("P", "M1", huge, huge))
		mustOK(t, e.RegisterItem("P", "I1", huge))
		for i := 0; i < history; i++ {
			_, err := e.Settle("P", Claim{
				ID:       fmt.Sprintf("H%d", i),
				MemberID: "M1",
				Details:  []Detail{{"I1", int64(i), 1}},
			})
			mustOK(t, err)
		}
		for i := 0; i < extraMembers; i++ {
			mustOK(t, e.RegisterMember("P", fmt.Sprintf("MX%d", i), huge, huge))
		}
		for i := 0; i < extraItems; i++ {
			mustOK(t, e.RegisterItem("P", fmt.Sprintf("IX%d", i), huge))
		}
		return e, e.policies["P"]
	}
	opsFor := func(e *Engine, p *Policy, details int) int64 {
		ds := make([]Detail, details)
		for i := range ds {
			ds[i] = Detail{ItemID: "I1", Day: int64(1<<29) + int64(i), Amount: 1}
		}
		before := p.ops
		_, err := e.Settle("P", Claim{ID: fmt.Sprintf("probe-%d", details), MemberID: "M1", Details: ds})
		mustOK(t, err)
		return p.ops - before
	}

	var base1, base3, base6 int64
	for i, cfg := range []struct{ history, members, items int }{
		{0, 0, 0},
		{1000, 500, 500},
		{20000, 5000, 5000},
	} {
		e, p := build(cfg.history, cfg.members, cfg.items)
		got1 := opsFor(e, p, 1)
		got3 := opsFor(e, p, 3)
		got6 := opsFor(e, p, 6)
		if i == 0 {
			base1, base3, base6 = got1, got3, got6
			continue
		}
		if got1 != base1 || got3 != base3 || got6 != base6 {
			t.Fatalf("结算开销随历史规模增长: history=%d members=%d items=%d -> ops=(%d,%d,%d), 基准=(%d,%d,%d)",
				cfg.history, cfg.members, cfg.items, got1, got3, got6, base1, base3, base6)
		}
	}
	// 账簿访问次数是明细数的一次函数，即只与本笔明细数有关。
	if 3*(base3-base1) != 2*(base6-base3) {
		t.Fatalf("结算开销应与明细数成线性关系: ops1=%d ops3=%d ops6=%d", base1, base3, base6)
	}
	perDetail := (base6 - base3) / 3
	t.Logf("每笔明细固定开销 %d 次账簿访问 (ops1=%d ops3=%d ops6=%d)", perDetail, base1, base3, base6)
}

// 墙钟佐证（需 -bench 运行）：历史规模 0 / 1k / 100k 下结算 4 明细理赔。
func BenchmarkSettle(b *testing.B) {
	const huge = int64(1) << 60
	for _, history := range []int{0, 1000, 100000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			e := NewEngine()
			if err := e.RegisterPolicy("P", 0, 1<<30, huge); err != nil {
				b.Fatal(err)
			}
			if err := e.RegisterMember("P", "M1", huge, huge); err != nil {
				b.Fatal(err)
			}
			if err := e.RegisterItem("P", "I1", huge); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < history; i++ {
				if _, err := e.Settle("P", Claim{
					ID:       fmt.Sprintf("H%d", i),
					MemberID: "M1",
					Details:  []Detail{{"I1", int64(i), 1}},
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				day := int64(1<<29) + int64(4*i)
				if _, err := e.Settle("P", Claim{
					ID:       fmt.Sprintf("B%d", i),
					MemberID: "M1",
					Details: []Detail{
						{"I1", day, 1}, {"I1", day + 1, 1},
						{"I1", day + 2, 1}, {"I1", day + 3, 1},
					},
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
