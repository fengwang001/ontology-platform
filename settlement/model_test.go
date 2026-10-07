package settlement

// 本文件包含一个独立编写的朴素模型（naive model），用于与正式实现对照。
// 朴素模型刻意采用不同的实现方式：保留全部历史记录、用切片线性扫描、
// 用普通 int64 算术（随机用例数值范围保证不溢出），汇总可从历史完整重算，
// 因此它也是“汇总可验证”的独立证据。

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

type naiveTerm struct {
	effDay, seq, amount, planDay int64
}

type naiveDefect struct {
	id      string
	forfeit int64
	open    bool
}

type naiveMilestone struct {
	amount, planDay int64
	terms           []naiveTerm
	accepted        bool
	passDay         int64
	overdue         int64
	settled         bool
	released        bool
	retention       int64
	defects         []naiveDefect
}

type naiveSettle struct {
	amount    int64
	retention int64
	advance   int64
	assessed  int64
	deducted  int64
	payment   int64
}

type naiveRelease struct {
	withheld int64
	forfeit  int64
	paid     int64
}

type naiveContract struct {
	spec            ContractSpec
	terminated      bool
	order           []string
	milestones      map[string]*naiveMilestone
	amendSeq        int64
	advanceDeducted int64
	assessedTotal   int64
	debt            int64
	settles         []naiveSettle
	releases        []naiveRelease
}

type naiveModel struct {
	lastNow   int64
	hasNow    bool
	contracts map[string]*naiveContract
	order     []string
}

func newNaiveModel() *naiveModel {
	return &naiveModel{contracts: make(map[string]*naiveContract)}
}

// summary 从完整历史重算汇总（O(历史)），与正式实现的 O(1) 增量汇总对照。
func (m *naiveModel) summary(contractID string) Summary {
	c := m.contracts[contractID]
	var s Summary
	for _, st := range c.settles {
		s.SettledAmountTotal += st.amount
		s.PaidTotal += st.payment
		s.RetentionHeld += st.retention
		s.AdvanceDeductedTotal += st.advance
		s.PenaltyDeductedTotal += st.deducted
	}
	for _, rl := range c.releases {
		s.RetentionHeld -= rl.withheld
		s.PaidTotal += rl.paid
		s.DefectForfeitTotal += rl.forfeit
	}
	var assessed, deducted int64
	for _, st := range c.settles {
		assessed += st.assessed
		deducted += st.deducted
	}
	s.OutstandingPenaltyDebt = assessed - deducted
	return s
}

func (m *naiveModel) checkClock(now int64) (ErrorCode, string) {
	if m.hasNow && now < m.lastNow {
		return ErrCodeClockRollback,
			fmt.Sprintf("时钟回退: now=%d < 上次被接受操作的 now=%d", now, m.lastNow)
	}
	return ErrCodeNone, ""
}

func (m *naiveModel) accept(now int64) {
	m.lastNow = now
	m.hasNow = true
}

// exec 执行一个操作，返回结果与判定依据（用于日志）。
func (m *naiveModel) exec(op Op) (Outcome, string) {
	switch op.Kind {
	case OpCreateContract:
		return m.execCreate(op)
	case OpAccept:
		return m.execAccept(op)
	case OpSettle:
		return m.execSettle(op)
	case OpRegisterDefect:
		return m.execRegisterDefect(op)
	case OpCloseDefect:
		return m.execCloseDefect(op)
	case OpRelease:
		return m.execRelease(op)
	case OpAmend:
		return m.execAmend(op)
	case OpTerminate:
		return m.execTerminate(op)
	}
	return Outcome{ErrCode: ErrCodeInvalidArgument}, "未知操作"
}

func (m *naiveModel) execCreate(op Op) (Outcome, string) {
	if op.ContractID == "" || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: 合同 ID 为空或 now 为负"
	}
	if code, why := naiveValidateSpec(op.Spec); code != ErrCodeNone {
		return Outcome{ErrCode: code}, "参数非法: " + why
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	if _, dup := m.contracts[op.ContractID]; dup {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 合同已存在"
	}
	c := &naiveContract{spec: op.Spec, milestones: make(map[string]*naiveMilestone)}
	for _, ms := range op.Spec.Milestones {
		c.milestones[ms.ID] = &naiveMilestone{amount: ms.Amount, planDay: ms.PlanDay}
		c.order = append(c.order, ms.ID)
	}
	m.contracts[op.ContractID] = c
	m.order = append(m.order, op.ContractID)
	m.accept(op.Now)
	return Outcome{}, fmt.Sprintf("创建合同 %s: 总额=%d 里程碑=%d 个", op.ContractID, op.Spec.TotalAmount, len(c.order))
}

func naiveValidateSpec(spec ContractSpec) (ErrorCode, string) {
	if spec.TotalAmount <= 0 {
		return ErrCodeInvalidArgument, "合同总金额非正"
	}
	if spec.AdvanceTotal < 0 {
		return ErrCodeInvalidArgument, "预付款总额为负"
	}
	if spec.AdvanceRatio < 0 || spec.AdvanceRatio > 10000 ||
		spec.RetentionRatio < 0 || spec.RetentionRatio > 10000 {
		return ErrCodeInvalidArgument, "抵扣/质保金比例越界"
	}
	if spec.RetentionRatio+spec.AdvanceRatio > 10000 {
		return ErrCodeInvalidArgument, "质保金与预付款抵扣比例之和超过 10000"
	}
	if spec.PenaltyDailyRate < 0 || spec.PenaltyDailyRate > 10000 ||
		spec.PenaltyCapRatio < 0 || spec.PenaltyCapRatio > 10000 {
		return ErrCodeInvalidArgument, "违约金费率/封顶比例越界"
	}
	if spec.WarrantyDays < 0 {
		return ErrCodeInvalidArgument, "质保期天数为负"
	}
	seen := map[string]bool{}
	var sum int64
	for _, ms := range spec.Milestones {
		if ms.ID == "" || seen[ms.ID] {
			return ErrCodeInvalidArgument, "里程碑 ID 为空或重复"
		}
		seen[ms.ID] = true
		if ms.Amount < 0 || ms.PlanDay < 0 {
			return ErrCodeInvalidArgument, "里程碑金额或计划日为负"
		}
		sum += ms.Amount
	}
	if sum > spec.TotalAmount {
		return ErrCodeInvalidArgument, "里程碑应付之和超过合同总金额"
	}
	return ErrCodeNone, ""
}

func (m *naiveModel) execAccept(op Op) (Outcome, string) {
	if op.ContractID == "" || op.MilestoneID == "" || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空或 now 为负"
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	ms, okm := c.milestones[op.MilestoneID]
	if !okm {
		return Outcome{ErrCode: ErrCodeNotFound}, "里程碑不存在"
	}
	if c.terminated {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 合同已终止"
	}
	if ms.accepted {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 已验收通过"
	}
	m.accept(op.Now)
	if !op.Passed {
		return Outcome{Accept: &AcceptResult{Passed: false, Amount: ms.amount, PlanDay: ms.planDay}},
			fmt.Sprintf("驳回: 计划日不变(%d)，可重新提交", ms.planDay)
	}
	// 适用 effectiveDay <= now 的最新条款。
	best := -1
	for i, t := range ms.terms {
		if t.effDay <= op.Now && (best < 0 ||
			t.effDay > ms.terms[best].effDay ||
			(t.effDay == ms.terms[best].effDay && t.seq > ms.terms[best].seq)) {
			best = i
		}
	}
	if best >= 0 {
		ms.amount = ms.terms[best].amount
		ms.planDay = ms.terms[best].planDay
	}
	ms.accepted = true
	ms.passDay = op.Now
	ms.overdue = 0
	if op.Now > ms.planDay {
		ms.overdue = op.Now - ms.planDay
	}
	return Outcome{Accept: &AcceptResult{Passed: true, Amount: ms.amount, PlanDay: ms.planDay, OverdueDays: ms.overdue}},
		fmt.Sprintf("验收通过: 适用条款(amount=%d planDay=%d)，逾期=%d 天", ms.amount, ms.planDay, ms.overdue)
}

func (m *naiveModel) execSettle(op Op) (Outcome, string) {
	if op.ContractID == "" || op.MilestoneID == "" || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空或 now 为负"
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	ms, okm := c.milestones[op.MilestoneID]
	if !okm {
		return Outcome{ErrCode: ErrCodeNotFound}, "里程碑不存在"
	}
	if c.terminated {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 合同已终止"
	}
	if !ms.accepted {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 未验收通过"
	}
	if ms.settled {
		return Outcome{ErrCode: ErrCodeAlreadySettled}, "重复结算"
	}
	amount := ms.amount
	retention := (amount*c.spec.RetentionRatio + 9999) / 10000 // 向上取整
	advance := amount * c.spec.AdvanceRatio / 10000            // 向下取整
	if remain := c.spec.AdvanceTotal - c.advanceDeducted; advance > remain {
		advance = remain
	}
	remaining := amount - retention - advance
	capAmount := c.spec.TotalAmount * c.spec.PenaltyCapRatio / 10000
	newPenalty := ms.overdue * c.spec.PenaltyDailyRate * c.spec.TotalAmount / 10000
	if room := capAmount - c.assessedTotal; newPenalty > room {
		newPenalty = room
	}
	debtIn := c.debt
	due := debtIn + newPenalty
	deduct := due
	if deduct > remaining {
		deduct = remaining
	}
	payment := remaining - deduct
	debtOut := due - deduct

	m.accept(op.Now)
	ms.settled = true
	ms.retention = retention
	c.advanceDeducted += advance
	c.assessedTotal += newPenalty
	c.debt = debtOut
	c.settles = append(c.settles, naiveSettle{
		amount: amount, retention: retention, advance: advance,
		assessed: newPenalty, deducted: deduct, payment: payment,
	})
	return Outcome{Settle: &SettleResult{
			Amount: amount, Retention: retention, AdvanceDeducted: advance,
			OverdueDays: ms.overdue, PenaltyAssessed: newPenalty,
			DebtCarriedIn: debtIn, PenaltyDeducted: deduct,
			DebtCarriedOut: debtOut, Payment: payment,
		}}, fmt.Sprintf("结算: 质保金=ceil(%d*%d/10000)=%d; 预付款=min(floor=%d, 剩余预付)=%d; "+
			"剩余=%d; 计提违约金=min(逾期%d*费率*总额/10000, 封顶余量)=%d; 欠额 %d→%d; 扣抵=%d; 实付=%d",
			amount, c.spec.RetentionRatio, retention,
			amount*c.spec.AdvanceRatio/10000, advance,
			remaining, ms.overdue, newPenalty, debtIn, debtOut, deduct, payment)
}

func (m *naiveModel) execRegisterDefect(op Op) (Outcome, string) {
	if op.ContractID == "" || op.MilestoneID == "" || op.DefectID == "" || op.Forfeit < 0 || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空、罚没为负或 now 为负"
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	ms, okm := c.milestones[op.MilestoneID]
	if !okm {
		return Outcome{ErrCode: ErrCodeNotFound}, "里程碑不存在"
	}
	if ms.released {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 质保金已释放"
	}
	for _, d := range ms.defects {
		if d.id == op.DefectID {
			return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 缺陷已登记"
		}
	}
	m.accept(op.Now)
	ms.defects = append(ms.defects, naiveDefect{id: op.DefectID, forfeit: op.Forfeit, open: true})
	return Outcome{}, fmt.Sprintf("登记缺陷 %s: 罚没=%d，暂停里程碑 %s 的质保金释放",
		op.DefectID, op.Forfeit, op.MilestoneID)
}

func (m *naiveModel) execCloseDefect(op Op) (Outcome, string) {
	if op.ContractID == "" || op.MilestoneID == "" || op.DefectID == "" || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空或 now 为负"
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	ms, okm := c.milestones[op.MilestoneID]
	if !okm {
		return Outcome{ErrCode: ErrCodeNotFound}, "里程碑不存在"
	}
	for i := range ms.defects {
		if ms.defects[i].id == op.DefectID {
			if !ms.defects[i].open {
				return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 缺陷已关闭"
			}
			m.accept(op.Now)
			ms.defects[i].open = false
			return Outcome{}, fmt.Sprintf("关闭缺陷 %s: 下一操作触及时可释放", op.DefectID)
		}
	}
	return Outcome{ErrCode: ErrCodeNotFound}, "缺陷不存在"
}

func (m *naiveModel) execRelease(op Op) (Outcome, string) {
	if op.ContractID == "" || op.MilestoneID == "" || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空或 now 为负"
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	ms, okm := c.milestones[op.MilestoneID]
	if !okm {
		return Outcome{ErrCode: ErrCodeNotFound}, "里程碑不存在"
	}
	if !ms.settled {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 未结算，无质保金"
	}
	if ms.released {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 质保金已释放"
	}
	expiry := ms.passDay + c.spec.WarrantyDays
	if op.Now <= expiry {
		return Outcome{ErrCode: ErrCodeInvalidState},
			fmt.Sprintf("状态不允许: 质保期未满(满日=%d)，满日次日方可释放", expiry)
	}
	openCount := 0
	var forfeitSum int64
	for _, d := range ms.defects {
		if d.open {
			openCount++
		}
		forfeitSum += d.forfeit
	}
	if openCount > 0 {
		return Outcome{ErrCode: ErrCodeInvalidState},
			fmt.Sprintf("状态不允许: %d 个未决缺陷，暂停释放", openCount)
	}
	forfeit := forfeitSum
	if forfeit > ms.retention {
		forfeit = ms.retention
	}
	paid := ms.retention - forfeit
	m.accept(op.Now)
	ms.released = true
	c.releases = append(c.releases, naiveRelease{withheld: ms.retention, forfeit: forfeit, paid: paid})
	return Outcome{Release: &ReleaseResult{Withheld: ms.retention, Forfeit: forfeit, PaidOut: paid}},
		fmt.Sprintf("释放: 质保金=%d; 缺陷罚没=min(登记合计%d, 质保金)=%d; 实付=%d",
			ms.retention, forfeitSum, forfeit, paid)
}

func (m *naiveModel) execAmend(op Op) (Outcome, string) {
	if op.ContractID == "" || op.Now < 0 || op.EffectiveDay < op.Now || len(op.Changes) == 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空、now 为负、生效日早于当前或变更单为空"
	}
	seen := map[string]bool{}
	for _, ch := range op.Changes {
		if ch.MilestoneID == "" || seen[ch.MilestoneID] || ch.Amount < 0 || ch.PlanDay < 0 {
			return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: 变更项非法"
		}
		seen[ch.MilestoneID] = true
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	for _, ch := range op.Changes {
		if _, okm := c.milestones[ch.MilestoneID]; !okm {
			return Outcome{ErrCode: ErrCodeNotFound}, "里程碑不存在"
		}
	}
	if c.terminated {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 合同已终止"
	}
	for _, ch := range op.Changes {
		if c.milestones[ch.MilestoneID].accepted {
			return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 已验收通过，变更不追溯"
		}
	}
	// 变更后各里程碑应付之和不得超过合同总金额。
	var sum int64
	for _, id := range c.order {
		ms := c.milestones[id]
		amt := naiveEventualAmount(ms)
		for _, ch := range op.Changes {
			if ch.MilestoneID == id && !ms.accepted {
				amt = naiveEventualAmountWith(ms, op.EffectiveDay, ch.Amount)
			}
		}
		sum += amt
	}
	if sum > c.spec.TotalAmount {
		return Outcome{ErrCode: ErrCodeChangeExceedsTotal},
			fmt.Sprintf("变更超出合同总额: 变更后应付之和 %d > 总额 %d", sum, c.spec.TotalAmount)
	}
	m.accept(op.Now)
	for _, ch := range op.Changes {
		ms := c.milestones[ch.MilestoneID]
		ms.terms = append(ms.terms, naiveTerm{
			effDay: op.EffectiveDay, seq: c.amendSeq, amount: ch.Amount, planDay: ch.PlanDay,
		})
	}
	c.amendSeq++
	return Outcome{}, fmt.Sprintf("变更生效: 生效日=%d，调整 %d 个里程碑", op.EffectiveDay, len(op.Changes))
}

func naiveEventualAmount(ms *naiveMilestone) int64 {
	if ms.accepted {
		return ms.amount
	}
	best := -1
	for i, t := range ms.terms {
		if best < 0 || t.effDay > ms.terms[best].effDay ||
			(t.effDay == ms.terms[best].effDay && t.seq > ms.terms[best].seq) {
			best = i
		}
	}
	if best >= 0 {
		return ms.terms[best].amount
	}
	return ms.amount
}

func naiveEventualAmountWith(ms *naiveMilestone, effDay, amount int64) int64 {
	if ms.accepted {
		return ms.amount
	}
	best := int64(-1)
	for _, t := range ms.terms {
		if t.effDay > best {
			best = t.effDay
		}
	}
	if effDay >= best {
		return amount
	}
	return naiveEventualAmount(ms)
}

func (m *naiveModel) execTerminate(op Op) (Outcome, string) {
	if op.ContractID == "" || op.Now < 0 {
		return Outcome{ErrCode: ErrCodeInvalidArgument}, "参数非法: ID 为空或 now 为负"
	}
	if code, why := m.checkClock(op.Now); code != ErrCodeNone {
		return Outcome{ErrCode: code}, why
	}
	c, okc := m.contracts[op.ContractID]
	if !okc {
		return Outcome{ErrCode: ErrCodeNotFound}, "合同不存在"
	}
	if c.terminated {
		return Outcome{ErrCode: ErrCodeInvalidState}, "状态不允许: 合同已终止"
	}
	m.accept(op.Now)
	c.terminated = true
	return Outcome{}, "终止合同: 已结算保持不变，未结算不再产生应付款，质保金照原规则释放"
}

// ---------- 随机操作生成器 ----------

type opGen struct {
	r     *rand.Rand
	model *naiveModel
}

// randomSpec 生成随机合同规格；小概率生成非法规格以覆盖参数校验。
func randomSpec(r *rand.Rand) ContractSpec {
	n := 1 + r.Intn(4)
	ms := make([]MilestoneSpec, 0, n)
	var total int64
	for i := 0; i < n; i++ {
		amt := int64(r.Intn(200_000))
		ms = append(ms, MilestoneSpec{
			ID:      fmt.Sprintf("m%d", i),
			Amount:  amt,
			PlanDay: int64(r.Intn(30)),
		})
		total += amt
	}
	spec := ContractSpec{
		TotalAmount:      total + int64(r.Intn(100_000)),
		AdvanceTotal:     int64(r.Intn(100_000)),
		AdvanceRatio:     int64(r.Intn(3000)),
		RetentionRatio:   int64(r.Intn(3000)),
		WarrantyDays:     int64(r.Intn(20)),
		PenaltyDailyRate: int64(r.Intn(200)),
		PenaltyCapRatio:  int64(r.Intn(10_000)),
		Milestones:       ms,
	}
	switch r.Intn(20) {
	case 0:
		spec.PenaltyCapRatio = 20_000 // 非法：比例越界
	case 1:
		spec.Milestones[0].Amount = -1 // 非法：金额为负
	case 2:
		spec.Milestones[0].ID = spec.Milestones[len(spec.Milestones)-1].ID // 非法：ID 重复
	case 3:
		spec.TotalAmount = 1 // 非法：里程碑应付之和超总额
	case 4:
		spec.RetentionRatio = 9_000 // 非法：与抵扣比例之和超 10000
	}
	return spec
}

func (g *opGen) pickContract() string {
	if len(g.model.order) == 0 || g.r.Intn(20) == 0 {
		return "ghost"
	}
	return g.model.order[g.r.Intn(len(g.model.order))]
}

func (g *opGen) pickMilestone(contractID string) string {
	c, ok := g.model.contracts[contractID]
	if !ok || len(c.order) == 0 || g.r.Intn(20) == 0 {
		return "ghost"
	}
	return c.order[g.r.Intn(len(c.order))]
}

// pickAmendableContract 偏向选择仍有未验收里程碑且未终止的合同。
func (g *opGen) pickAmendableContract() string {
	if g.r.Intn(10) < 4 {
		return g.pickContract()
	}
	var cands []string
	for _, cid := range g.model.order {
		c := g.model.contracts[cid]
		if c.terminated {
			continue
		}
		for _, mid := range c.order {
			if !c.milestones[mid].accepted {
				cands = append(cands, cid)
				break
			}
		}
	}
	if len(cands) == 0 {
		return g.pickContract()
	}
	return cands[g.r.Intn(len(cands))]
}

// pickUnacceptedMilestone 偏向选择未验收通过的里程碑。
func (g *opGen) pickUnacceptedMilestone(contractID string) string {
	c, ok := g.model.contracts[contractID]
	if !ok || g.r.Intn(10) < 3 {
		return g.pickMilestone(contractID)
	}
	var cands []string
	for _, mid := range c.order {
		if !c.milestones[mid].accepted {
			cands = append(cands, mid)
		}
	}
	if len(cands) == 0 {
		return g.pickMilestone(contractID)
	}
	return cands[g.r.Intn(len(cands))]
}

// pickSettleTarget 偏向选择可结算的里程碑，提高被接受操作的覆盖率。
func (g *opGen) pickSettleTarget() (string, string, bool) {
	var cands [][2]string
	for _, cid := range g.model.order {
		c := g.model.contracts[cid]
		if c.terminated {
			continue
		}
		for _, mid := range c.order {
			if ms := c.milestones[mid]; ms.accepted && !ms.settled {
				cands = append(cands, [2]string{cid, mid})
			}
		}
	}
	if len(cands) == 0 {
		return "", "", false
	}
	p := cands[g.r.Intn(len(cands))]
	return p[0], p[1], true
}

func (g *opGen) pickReleaseTarget() (string, string, bool) {
	var cands [][2]string
	for _, cid := range g.model.order {
		c := g.model.contracts[cid]
		for _, mid := range c.order {
			if ms := c.milestones[mid]; ms.settled && !ms.released {
				cands = append(cands, [2]string{cid, mid})
			}
		}
	}
	if len(cands) == 0 {
		return "", "", false
	}
	p := cands[g.r.Intn(len(cands))]
	return p[0], p[1], true
}

func (g *opGen) next(clock int64) (Op, int64) {
	r := g.r
	now := clock + int64(r.Intn(4))
	if r.Intn(10) == 0 {
		now = clock - int64(r.Intn(6)) // 可能触发时钟回退或负 now
	}
	newClock := clock
	if now > newClock {
		newClock = now
	}
	kind := r.Intn(100)
	switch {
	case kind < 22: // 验收
		cid := g.pickContract()
		return Op{Kind: OpAccept, ContractID: cid, MilestoneID: g.pickMilestone(cid),
			Passed: r.Intn(10) < 7, Now: now}, newClock
	case kind < 42: // 结算
		if r.Intn(10) < 6 {
			if cid, mid, ok := g.pickSettleTarget(); ok {
				return Op{Kind: OpSettle, ContractID: cid, MilestoneID: mid, Now: now}, newClock
			}
		}
		cid := g.pickContract()
		return Op{Kind: OpSettle, ContractID: cid, MilestoneID: g.pickMilestone(cid), Now: now}, newClock
	case kind < 52: // 登记缺陷
		cid := g.pickContract()
		forfeit := int64(r.Intn(50_000))
		if r.Intn(20) == 0 {
			forfeit = -1 // 非法
		}
		return Op{Kind: OpRegisterDefect, ContractID: cid, MilestoneID: g.pickMilestone(cid),
			DefectID: fmt.Sprintf("d%d", r.Intn(20)), Forfeit: forfeit, Now: now}, newClock
	case kind < 60: // 关闭缺陷
		cid := g.pickContract()
		return Op{Kind: OpCloseDefect, ContractID: cid, MilestoneID: g.pickMilestone(cid),
			DefectID: fmt.Sprintf("d%d", r.Intn(20)), Now: now}, newClock
	case kind < 72: // 释放质保金
		if r.Intn(10) < 6 {
			if cid, mid, ok := g.pickReleaseTarget(); ok {
				return Op{Kind: OpRelease, ContractID: cid, MilestoneID: mid, Now: now}, newClock
			}
		}
		cid := g.pickContract()
		return Op{Kind: OpRelease, ContractID: cid, MilestoneID: g.pickMilestone(cid), Now: now}, newClock
	case kind < 82: // 变更
		cid := g.pickAmendableContract()
		nCh := 1 + r.Intn(2)
		changes := make([]Change, 0, nCh)
		for i := 0; i < nCh; i++ {
			changes = append(changes, Change{
				MilestoneID: g.pickUnacceptedMilestone(cid),
				Amount:      int64(r.Intn(600_000)),
				PlanDay:     int64(r.Intn(40)),
			})
		}
		effDay := now + int64(r.Intn(3))
		if r.Intn(10) == 0 {
			effDay = now - 1 // 非法：生效日早于当前
		}
		return Op{Kind: OpAmend, ContractID: cid, EffectiveDay: effDay, Changes: changes, Now: now}, newClock
	case kind < 86: // 终止
		return Op{Kind: OpTerminate, ContractID: g.pickContract(), Now: now}, newClock
	case kind < 90: // 再建合同（小概率重复 ID）
		n := len(g.model.order) + r.Intn(2)
		return Op{Kind: OpCreateContract, ContractID: fmt.Sprintf("c%d", n),
			Spec: randomSpec(r), Now: now}, newClock
	default: // 其余再验收/结算
		cid := g.pickContract()
		return Op{Kind: OpAccept, ContractID: cid, MilestoneID: g.pickMilestone(cid),
			Passed: r.Intn(10) < 5, Now: now}, newClock
	}
}

// ---------- 对照测试 ----------

func runStep(t *testing.T, svc *Service, model *naiveModel, step int, op Op) {
	t.Helper()
	got := svc.Execute(op)
	want, rationale := model.exec(op)
	t.Logf("step=%d 输入=%s 输出=%s 判定依据=%s", step, op, got.ErrCode, rationale)
	if got.ErrCode != want.ErrCode {
		t.Fatalf("step=%d %s: 错误码不一致: 服务=%s 模型=%s (依据: %s)",
			step, op, got.ErrCode, want.ErrCode, rationale)
	}
	if !reflect.DeepEqual(got.Accept, want.Accept) ||
		!reflect.DeepEqual(got.Settle, want.Settle) ||
		!reflect.DeepEqual(got.Release, want.Release) {
		t.Fatalf("step=%d %s: 结果不一致:\n服务=%+v/%+v/%+v\n模型=%+v/%+v/%+v",
			step, op, got.Accept, got.Settle, got.Release,
			want.Accept, want.Settle, want.Release)
	}
	for _, id := range model.order {
		sgot, err := svc.Summary(id)
		if err != nil {
			t.Fatalf("step=%d 合同 %s 汇总查询失败: %v", step, id, err)
		}
		swant := model.summary(id)
		if sgot != swant {
			t.Fatalf("step=%d 合同 %s 汇总不一致:\n服务=%+v\n模型=%+v", step, id, sgot, swant)
		}
		if !sgot.Conserved() {
			t.Fatalf("step=%d 合同 %s 守恒被破坏: %+v", step, id, sgot)
		}
	}
}

// TestNaiveModelDifferential 与独立编写的朴素模型对照大量随机操作序列，
// 每步打印输入、输出与判定依据，并逐步校验汇总一致与守恒。
func TestNaiveModelDifferential(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			svc := NewService()
			rec := &Recorder{}
			svc.SetRecorder(rec)
			model := newNaiveModel()
			g := &opGen{r: r, model: model}
			clock := int64(0)
			step := 0
			for i, n := 0, 1+r.Intn(3); i < n; i++ {
				runStep(t, svc, model, step, Op{Kind: OpCreateContract,
					ContractID: fmt.Sprintf("c%d", i), Spec: randomSpec(r), Now: clock})
				step++
			}
			for ; step < 200; step++ {
				op, newClock := g.next(clock)
				clock = newClock
				runStep(t, svc, model, step, op)
			}
			// 相同操作序列重放应得到完全相同的结果。
			replayed, err := Replay(rec.Snapshot())
			if err != nil {
				t.Fatalf("重放失败: %v", err)
			}
			for _, id := range model.order {
				a, err := svc.Summary(id)
				if err != nil {
					t.Fatal(err)
				}
				b, err := replayed.Summary(id)
				if err != nil {
					t.Fatal(err)
				}
				if a != b {
					t.Fatalf("合同 %s 重放后汇总不一致:\n原=%+v\n放=%+v", id, a, b)
				}
			}
		})
	}
}

// ---------- 并发等价测试 ----------

// TestConcurrentEquivalence 并发调用等价于某个串行顺序：
// 服务在锁内记录实际操作顺序，重放该顺序必须得到完全相同的结果与汇总。
func TestConcurrentEquivalence(t *testing.T) {
	svc := NewService()
	rec := &Recorder{}
	svc.SetRecorder(rec)
	for i := 0; i < 4; i++ {
		spec := ContractSpec{
			TotalAmount:      1_000_000,
			AdvanceTotal:     50_000,
			AdvanceRatio:     1000,
			RetentionRatio:   500,
			WarrantyDays:     10,
			PenaltyDailyRate: 20,
			PenaltyCapRatio:  1000,
			Milestones: []MilestoneSpec{
				{ID: "m0", Amount: 100_000, PlanDay: 110},
				{ID: "m1", Amount: 150_000, PlanDay: 120},
				{ID: "m2", Amount: 200_000, PlanDay: 130},
				{ID: "m3", Amount: 250_000, PlanDay: 140},
			},
		}
		if err := svc.CreateContract(fmt.Sprintf("c%d", i), spec, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	var counter atomic.Int64
	counter.Store(100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				svc.Execute(randomConcurrentOp(r, &counter))
			}
		}(int64(g)*7919 + 1)
	}
	wg.Wait()

	replayed, err := Replay(rec.Snapshot())
	if err != nil {
		t.Fatalf("并发执行不等价于任何串行顺序: %v", err)
	}
	for _, id := range svc.ContractIDs() {
		a, err := svc.Summary(id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := replayed.Summary(id)
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("合同 %s 并发与重放汇总不一致:\n并发=%+v\n重放=%+v", id, a, b)
		}
		if !a.Conserved() {
			t.Fatalf("合同 %s 守恒被破坏: %+v", id, a)
		}
	}
}

// randomConcurrentOp 生成并发随机操作。now 取自共享计数器，
// 不同 goroutine 的取值与执行顺序天然交错，会真实发生时钟回退拒绝。
func randomConcurrentOp(r *rand.Rand, counter *atomic.Int64) Op {
	now := counter.Add(int64(1 + r.Intn(3)))
	cid := fmt.Sprintf("c%d", r.Intn(4))
	mid := fmt.Sprintf("m%d", r.Intn(4))
	switch r.Intn(10) {
	case 0, 1, 2:
		return Op{Kind: OpAccept, ContractID: cid, MilestoneID: mid, Passed: r.Intn(10) < 7, Now: now}
	case 3, 4, 5:
		return Op{Kind: OpSettle, ContractID: cid, MilestoneID: mid, Now: now}
	case 6:
		return Op{Kind: OpRegisterDefect, ContractID: cid, MilestoneID: mid,
			DefectID: fmt.Sprintf("d%d", r.Intn(10)), Forfeit: int64(r.Intn(5_000)), Now: now}
	case 7:
		return Op{Kind: OpCloseDefect, ContractID: cid, MilestoneID: mid,
			DefectID: fmt.Sprintf("d%d", r.Intn(10)), Now: now}
	case 8:
		return Op{Kind: OpRelease, ContractID: cid, MilestoneID: mid, Now: now}
	default:
		return Op{Kind: OpAmend, ContractID: cid, EffectiveDay: now,
			Changes: []Change{{MilestoneID: mid, Amount: int64(r.Intn(300_000)), PlanDay: int64(100 + r.Intn(100))}},
			Now:     now,
		}
	}
}
