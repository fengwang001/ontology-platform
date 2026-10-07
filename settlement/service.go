package settlement

import (
	"math"
	"math/big"
	"sort"
	"sync"
)

// termChange 变更单对单个里程碑设定的条款，自 effectiveDay 起对之后验收适用。
type termChange struct {
	effectiveDay int64
	seq          int64 // 合同内变更单受理序号，用于同生效日的先后次序
	amount       int64
	planDay      int64
}

// defect 缺陷登记。
type defect struct {
	forfeit int64
	open    bool
}

// milestone 里程碑运行态。
type milestone struct {
	amount  int64 // 当前基准应付金额（验收通过时被适用条款覆盖）
	planDay int64 // 当前基准计划验收日
	terms   []termChange

	accepted bool
	passDay  int64
	overdue  int64

	settled   bool
	released  bool
	retention int64 // 结算时扣留的质保金

	defects          map[string]*defect
	openDefects      int64 // 未决缺陷数（O(1) 判定暂停释放）
	defectForfeitSum int64 // 已登记缺陷罚没累计（O(1) 释放扣抵）
}

// latestTerms 返回 (effectiveDay, seq) 最大的条款。
func (m *milestone) latestTerms() (termChange, bool) {
	var best termChange
	found := false
	for _, t := range m.terms {
		if !found || t.effectiveDay > best.effectiveDay ||
			(t.effectiveDay == best.effectiveDay && t.seq > best.seq) {
			best = t
			found = true
		}
	}
	return best, found
}

// eventualAmount 用于变更总额校验：已验收的按实际验收额，未验收的按最新条款。
func (m *milestone) eventualAmount() int64 {
	if m.accepted {
		return m.amount
	}
	if t, ok := m.latestTerms(); ok {
		return t.amount
	}
	return m.amount
}

// eventualAmountWith 计算若再追加一条 (effDay, amount) 条款后的最终应付金额。
// 新条款的 seq 大于所有现有条款，故生效日不早于现有最新条款时新条款胜出。
func (m *milestone) eventualAmountWith(effDay, amount int64) int64 {
	if m.accepted {
		return m.amount
	}
	if t, ok := m.latestTerms(); ok && t.effectiveDay > effDay {
		return t.amount
	}
	return amount
}

// applyTerms 在验收通过日 day 适用所有 effectiveDay <= day 的条款中最新者。
func (m *milestone) applyTerms(day int64) {
	var best termChange
	found := false
	for _, t := range m.terms {
		if t.effectiveDay > day {
			continue
		}
		if !found || t.effectiveDay > best.effectiveDay ||
			(t.effectiveDay == best.effectiveDay && t.seq > best.seq) {
			best = t
			found = true
		}
	}
	if found {
		m.amount = best.amount
		m.planDay = best.planDay
	}
}

// contract 合同运行态。所有汇总字段均为增量维护的 O(1) 累计值。
type contract struct {
	id   string
	spec ContractSpec

	milestones map[string]*milestone
	order      []string // 里程碑 ID 的稳定顺序（创建顺序）

	terminated bool
	amendSeq   int64

	advanceDeducted int64 // 累计预付款抵扣
	penaltyAssessed int64 // 累计计提违约金（已扣抵 + 欠额），受封顶约束
	penaltyDeducted int64 // 累计违约金扣抵
	penaltyDebt     int64 // 未扣抵违约金欠额

	paidTotal     int64 // 累计实付（含质保金释放支付）
	retentionHeld int64 // 质保金余额
	defectForfeit int64 // 累计缺陷罚没扣抵
	settledAmount int64 // 已结算里程碑应付之和
}

func (c *contract) penaltyCap() int64 {
	return mulDivFloor(c.spec.TotalAmount, c.spec.PenaltyCapRatio, ratioBase)
}

// Service 采购合同履约付款结算服务。所有公开方法可并发调用。
type Service struct {
	mu        sync.Mutex
	contracts map[string]*contract
	lastNow   int64 // 上一次被接受操作的 now
	hasNow    bool
	recorder  *Recorder
}

// NewService 创建空服务。
func NewService() *Service {
	return &Service{contracts: make(map[string]*contract)}
}

// SetRecorder 挂载操作记录器（审计与重放用），传 nil 关闭。
// 记录器仅在操作结束后追加一条定长记录，不改变任何操作的渐近复杂度。
func (s *Service) SetRecorder(r *Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorder = r
}

// checkClock 校验时钟回退（须持锁调用）。
func (s *Service) checkClock(now int64) *Error {
	if s.hasNow && now < s.lastNow {
		return clockRollback("now=%d 小于上一次被接受操作的 now=%d", now, s.lastNow)
	}
	return nil
}

// acceptClock 接受操作时推进时钟（须持锁调用）。
func (s *Service) acceptClock(now int64) {
	s.lastNow = now
	s.hasNow = true
}

// validateSpec 校验合同规格，固定检查顺序以保证错误信息确定。
func validateSpec(spec ContractSpec) *Error {
	if spec.TotalAmount <= 0 {
		return invalidArg("合同总金额必须为正: %d", spec.TotalAmount)
	}
	if spec.AdvanceTotal < 0 {
		return invalidArg("预付款总额为负: %d", spec.AdvanceTotal)
	}
	if spec.AdvanceRatio < 0 || spec.AdvanceRatio > ratioBase {
		return invalidArg("预付款抵扣比例越界 [0,10000]: %d", spec.AdvanceRatio)
	}
	if spec.RetentionRatio < 0 || spec.RetentionRatio > ratioBase {
		return invalidArg("质保金比例越界 [0,10000]: %d", spec.RetentionRatio)
	}
	if spec.RetentionRatio+spec.AdvanceRatio > ratioBase {
		return invalidArg("质保金比例与预付款抵扣比例之和超过 10000: %d+%d",
			spec.RetentionRatio, spec.AdvanceRatio)
	}
	if spec.PenaltyDailyRate < 0 || spec.PenaltyDailyRate > ratioBase {
		return invalidArg("违约金日费率越界 [0,10000]: %d", spec.PenaltyDailyRate)
	}
	if spec.PenaltyCapRatio < 0 || spec.PenaltyCapRatio > ratioBase {
		return invalidArg("违约金封顶比例越界 [0,10000]: %d", spec.PenaltyCapRatio)
	}
	if spec.WarrantyDays < 0 {
		return invalidArg("质保期天数为负: %d", spec.WarrantyDays)
	}
	seen := make(map[string]bool, len(spec.Milestones))
	var sum int64
	for _, ms := range spec.Milestones {
		if ms.ID == "" {
			return invalidArg("里程碑 ID 为空")
		}
		if seen[ms.ID] {
			return invalidArg("里程碑 ID 重复: %q", ms.ID)
		}
		seen[ms.ID] = true
		if ms.Amount < 0 {
			return invalidArg("里程碑 %q 应付金额为负: %d", ms.ID, ms.Amount)
		}
		if ms.PlanDay < 0 {
			return invalidArg("里程碑 %q 计划验收日为负: %d", ms.ID, ms.PlanDay)
		}
		sum += ms.Amount
	}
	if sum > spec.TotalAmount {
		return invalidArg("里程碑应付之和 %d 超过合同总金额 %d", sum, spec.TotalAmount)
	}
	return nil
}

// createContract 创建合同。
func (s *Service) createContract(op Op) Outcome {
	id, spec, now := op.ContractID, op.Spec, op.Now
	if id == "" {
		return fail(invalidArg("合同 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := validateSpec(spec); err != nil {
		return fail(err)
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	if _, dup := s.contracts[id]; dup {
		return fail(invalidState("合同 %q 已存在", id))
	}
	c := &contract{
		id:         id,
		spec:       spec,
		milestones: make(map[string]*milestone, len(spec.Milestones)),
	}
	for _, ms := range spec.Milestones {
		c.milestones[ms.ID] = &milestone{
			amount:  ms.Amount,
			planDay: ms.PlanDay,
			defects: make(map[string]*defect),
		}
		c.order = append(c.order, ms.ID)
	}
	s.contracts[id] = c
	s.acceptClock(now)
	return ok()
}

// acceptMilestone 里程碑验收：通过与驳回两种结果。
// 驳回不改变计划日，供应商可重新提交再验收；通过后按验收日适用条款并计算逾期。
func (s *Service) acceptMilestone(op Op) Outcome {
	cid, mid, passed, now := op.ContractID, op.MilestoneID, op.Passed, op.Now
	if cid == "" || mid == "" {
		return fail(invalidArg("合同或里程碑 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	m, okm := c.milestones[mid]
	if !okm {
		return fail(notFound("里程碑 %q 不存在于合同 %q", mid, cid))
	}
	if c.terminated {
		return fail(invalidState("合同 %q 已终止，不再验收", cid))
	}
	if m.accepted {
		return fail(invalidState("里程碑 %q 已验收通过", mid))
	}
	s.acceptClock(now)
	if passed {
		m.applyTerms(now)
		m.accepted = true
		m.passDay = now
		if now > m.planDay {
			m.overdue = now - m.planDay
		} else {
			m.overdue = 0
		}
	}
	return Outcome{Accept: &AcceptResult{
		Passed:      passed,
		Amount:      m.amount,
		PlanDay:     m.planDay,
		OverdueDays: m.overdue,
	}}
}

// settleMilestone 结算一个已验收通过的里程碑。
// 固定顺序：质保金扣留（向上取整）→ 预付款抵扣（向下取整、累计不超预付款总额）
// → 违约金（含结转欠额优先扣抵、累计受封顶约束）→ 实付（不为负）。
func (s *Service) settleMilestone(op Op) Outcome {
	cid, mid, now := op.ContractID, op.MilestoneID, op.Now
	if cid == "" || mid == "" {
		return fail(invalidArg("合同或里程碑 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	m, okm := c.milestones[mid]
	if !okm {
		return fail(notFound("里程碑 %q 不存在于合同 %q", mid, cid))
	}
	if c.terminated {
		return fail(invalidState("合同 %q 已终止，未结算里程碑不再产生应付款", cid))
	}
	if !m.accepted {
		return fail(invalidState("里程碑 %q 尚未验收通过", mid))
	}
	if m.settled {
		return fail(alreadySettled("里程碑 %q 已结算", mid))
	}

	amount := m.amount
	// 1. 质保金：以应付全额为基数向上取整。
	retention := mulDivCeil(amount, c.spec.RetentionRatio, ratioBase)
	// 2. 预付款抵扣：以应付全额为基数向下取整，累计不得超过预付款总额。
	advance := mulDivFloor(amount, c.spec.AdvanceRatio, ratioBase)
	if remain := c.spec.AdvanceTotal - c.advanceDeducted; advance > remain {
		advance = remain
	}
	// 比例和不超过 10000 保证 remaining 非负。
	remaining := amount - retention - advance
	// 3. 违约金：本次计提受累计封顶约束；欠额优先于本次计提扣抵。
	newPenalty := penaltyOf(m.overdue, c.spec.PenaltyDailyRate, c.spec.TotalAmount)
	if room := c.penaltyCap() - c.penaltyAssessed; newPenalty > room {
		newPenalty = room
	}
	debtIn := c.penaltyDebt
	due := debtIn + newPenalty
	deduct := due
	if deduct > remaining {
		deduct = remaining
	}
	payment := remaining - deduct
	debtOut := due - deduct

	s.acceptClock(now)
	m.settled = true
	m.retention = retention
	c.advanceDeducted += advance
	c.penaltyAssessed += newPenalty
	c.penaltyDeducted += deduct
	c.penaltyDebt = debtOut
	c.retentionHeld += retention
	c.paidTotal += payment
	c.settledAmount += amount

	return Outcome{Settle: &SettleResult{
		Amount:          amount,
		Retention:       retention,
		AdvanceDeducted: advance,
		OverdueDays:     m.overdue,
		PenaltyAssessed: newPenalty,
		DebtCarriedIn:   debtIn,
		PenaltyDeducted: deduct,
		DebtCarriedOut:  debtOut,
		Payment:         payment,
	}}
}

// releaseRetention 质保金释放：满日次日可释放；存在未决缺陷则暂停；
// 释放前登记的缺陷罚没从该里程碑质保金中扣抵（不超过该质保金）。
// 合同终止后仍按原规则释放。
func (s *Service) releaseRetention(op Op) Outcome {
	cid, mid, now := op.ContractID, op.MilestoneID, op.Now
	if cid == "" || mid == "" {
		return fail(invalidArg("合同或里程碑 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	m, okm := c.milestones[mid]
	if !okm {
		return fail(notFound("里程碑 %q 不存在于合同 %q", mid, cid))
	}
	if !m.settled {
		return fail(invalidState("里程碑 %q 尚未结算，无质保金可释放", mid))
	}
	if m.released {
		return fail(invalidState("里程碑 %q 质保金已释放", mid))
	}
	expiry := saturatingAdd(m.passDay, c.spec.WarrantyDays)
	if now <= expiry {
		return fail(invalidState("里程碑 %q 质保期未满：满日为 %d，满日次日方可释放", mid, expiry))
	}
	if m.openDefects > 0 {
		return fail(invalidState("里程碑 %q 存在 %d 个未决缺陷，暂停释放", mid, m.openDefects))
	}
	forfeit := m.defectForfeitSum
	if forfeit > m.retention {
		forfeit = m.retention
	}
	paid := m.retention - forfeit

	s.acceptClock(now)
	m.released = true
	c.retentionHeld -= m.retention
	c.defectForfeit += forfeit
	c.paidTotal += paid

	return Outcome{Release: &ReleaseResult{
		Withheld: m.retention,
		Forfeit:  forfeit,
		PaidOut:  paid,
	}}
}

// registerDefect 对单个里程碑登记缺陷，只暂停该里程碑的质保金释放。
func (s *Service) registerDefect(op Op) Outcome {
	cid, mid, did, forfeit, now := op.ContractID, op.MilestoneID, op.DefectID, op.Forfeit, op.Now
	if cid == "" || mid == "" || did == "" {
		return fail(invalidArg("合同、里程碑或缺陷 ID 为空"))
	}
	if forfeit < 0 {
		return fail(invalidArg("缺陷罚没金额为负: %d", forfeit))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	m, okm := c.milestones[mid]
	if !okm {
		return fail(notFound("里程碑 %q 不存在于合同 %q", mid, cid))
	}
	if m.released {
		return fail(invalidState("里程碑 %q 质保金已释放，不能再登记缺陷", mid))
	}
	if _, dup := m.defects[did]; dup {
		return fail(invalidState("缺陷 %q 已登记", did))
	}
	s.acceptClock(now)
	m.defects[did] = &defect{forfeit: forfeit, open: true}
	m.openDefects++
	m.defectForfeitSum += forfeit
	return ok()
}

// closeDefect 关闭缺陷；关闭后的下一个操作触及时方可释放质保金。
func (s *Service) closeDefect(op Op) Outcome {
	cid, mid, did, now := op.ContractID, op.MilestoneID, op.DefectID, op.Now
	if cid == "" || mid == "" || did == "" {
		return fail(invalidArg("合同、里程碑或缺陷 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	m, okm := c.milestones[mid]
	if !okm {
		return fail(notFound("里程碑 %q 不存在于合同 %q", mid, cid))
	}
	d, okd := m.defects[did]
	if !okd {
		return fail(notFound("缺陷 %q 不存在于里程碑 %q", did, mid))
	}
	if !d.open {
		return fail(invalidState("缺陷 %q 已关闭", did))
	}
	s.acceptClock(now)
	d.open = false
	m.openDefects--
	return ok()
}

// amendContract 合同变更：只调整尚未验收通过里程碑的应付金额与计划日，
// 自生效日起对之后验收的里程碑适用；已验收通过或已结算的不追溯。
// 变更后各里程碑应付金额之和不得超过合同总金额。预付款总额不可变更。
func (s *Service) amendContract(op Op) Outcome {
	cid, effDay, changes, now := op.ContractID, op.EffectiveDay, op.Changes, op.Now
	if cid == "" {
		return fail(invalidArg("合同 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if effDay < now {
		return fail(invalidArg("生效日 %d 早于当前操作日 %d", effDay, now))
	}
	if len(changes) == 0 {
		return fail(invalidArg("变更单为空"))
	}
	seen := make(map[string]bool, len(changes))
	for _, ch := range changes {
		if ch.MilestoneID == "" {
			return fail(invalidArg("变更单中里程碑 ID 为空"))
		}
		if seen[ch.MilestoneID] {
			return fail(invalidArg("变更单中里程碑 %q 重复", ch.MilestoneID))
		}
		seen[ch.MilestoneID] = true
		if ch.Amount < 0 {
			return fail(invalidArg("变更后里程碑 %q 应付金额为负: %d", ch.MilestoneID, ch.Amount))
		}
		if ch.PlanDay < 0 {
			return fail(invalidArg("变更后里程碑 %q 计划验收日为负: %d", ch.MilestoneID, ch.PlanDay))
		}
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	for _, ch := range changes {
		if _, okm := c.milestones[ch.MilestoneID]; !okm {
			return fail(notFound("里程碑 %q 不存在于合同 %q", ch.MilestoneID, cid))
		}
	}
	if c.terminated {
		return fail(invalidState("合同 %q 已终止，不可变更", cid))
	}
	for _, ch := range changes {
		if c.milestones[ch.MilestoneID].accepted {
			return fail(invalidState("里程碑 %q 已验收通过，变更不追溯", ch.MilestoneID))
		}
	}
	// 变更后各里程碑应付金额之和不得超过合同总金额。
	var sum int64
	for _, id := range c.order {
		m := c.milestones[id]
		amt := m.eventualAmount()
		if ch, targeted := findChange(changes, id); targeted {
			amt = m.eventualAmountWith(effDay, ch.Amount)
		}
		sum += amt
	}
	if sum > c.spec.TotalAmount {
		return fail(changeExceedsTotal("变更后里程碑应付之和 %d 超过合同总金额 %d",
			sum, c.spec.TotalAmount))
	}
	s.acceptClock(now)
	for _, ch := range changes {
		m := c.milestones[ch.MilestoneID]
		m.terms = append(m.terms, termChange{
			effectiveDay: effDay,
			seq:          c.amendSeq,
			amount:       ch.Amount,
			planDay:      ch.PlanDay,
		})
	}
	c.amendSeq++
	return ok()
}

func findChange(changes []Change, milestoneID string) (Change, bool) {
	for _, ch := range changes {
		if ch.MilestoneID == milestoneID {
			return ch, true
		}
	}
	return Change{}, false
}

// terminateContract 终止合同：已结算的保持不变，未结算的不再产生应付款，
// 已扣留的质保金仍按原规则释放，欠额不再扣抵。
func (s *Service) terminateContract(op Op) Outcome {
	cid, now := op.ContractID, op.Now
	if cid == "" {
		return fail(invalidArg("合同 ID 为空"))
	}
	if now < 0 {
		return fail(invalidArg("now=%d 为负", now))
	}
	if err := s.checkClock(now); err != nil {
		return fail(err)
	}
	c, okc := s.contracts[cid]
	if !okc {
		return fail(notFound("合同 %q 不存在", cid))
	}
	if c.terminated {
		return fail(invalidState("合同 %q 已终止", cid))
	}
	s.acceptClock(now)
	c.terminated = true
	return ok()
}

// Execute 在服务锁下执行一个操作并记录到记录器（若已挂载）。
// 所有操作经此唯一入口串行化，故并发调用等价于某个串行顺序。
func (s *Service) Execute(op Op) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.executeLocked(op)
	if s.recorder != nil {
		s.recorder.append(OpEvent{Op: op, Outcome: out})
	}
	return out
}

func (s *Service) executeLocked(op Op) Outcome {
	switch op.Kind {
	case OpCreateContract:
		return s.createContract(op)
	case OpAccept:
		return s.acceptMilestone(op)
	case OpSettle:
		return s.settleMilestone(op)
	case OpRegisterDefect:
		return s.registerDefect(op)
	case OpCloseDefect:
		return s.closeDefect(op)
	case OpRelease:
		return s.releaseRetention(op)
	case OpAmend:
		return s.amendContract(op)
	case OpTerminate:
		return s.terminateContract(op)
	default:
		return fail(invalidArg("未知操作类型 %d", int(op.Kind)))
	}
}

// CreateContract 创建合同。
func (s *Service) CreateContract(contractID string, spec ContractSpec, now int64) error {
	return s.Execute(Op{Kind: OpCreateContract, ContractID: contractID, Spec: spec, Now: now}).err()
}

// AcceptMilestone 提交里程碑验收结果（passed=true 通过，false 驳回）。
func (s *Service) AcceptMilestone(contractID, milestoneID string, passed bool, now int64) (*AcceptResult, error) {
	out := s.Execute(Op{Kind: OpAccept, ContractID: contractID, MilestoneID: milestoneID, Passed: passed, Now: now})
	return out.Accept, out.err()
}

// SettleMilestone 结算一个已验收通过的里程碑。
func (s *Service) SettleMilestone(contractID, milestoneID string, now int64) (*SettleResult, error) {
	out := s.Execute(Op{Kind: OpSettle, ContractID: contractID, MilestoneID: milestoneID, Now: now})
	return out.Settle, out.err()
}

// RegisterDefect 对单个里程碑登记缺陷。
func (s *Service) RegisterDefect(contractID, milestoneID, defectID string, forfeit int64, now int64) error {
	return s.Execute(Op{Kind: OpRegisterDefect, ContractID: contractID, MilestoneID: milestoneID,
		DefectID: defectID, Forfeit: forfeit, Now: now}).err()
}

// CloseDefect 关闭缺陷。
func (s *Service) CloseDefect(contractID, milestoneID, defectID string, now int64) error {
	return s.Execute(Op{Kind: OpCloseDefect, ContractID: contractID, MilestoneID: milestoneID,
		DefectID: defectID, Now: now}).err()
}

// ReleaseRetention 释放单个里程碑的质保金。
func (s *Service) ReleaseRetention(contractID, milestoneID string, now int64) (*ReleaseResult, error) {
	out := s.Execute(Op{Kind: OpRelease, ContractID: contractID, MilestoneID: milestoneID, Now: now})
	return out.Release, out.err()
}

// AmendContract 提交变更单。
func (s *Service) AmendContract(contractID string, effectiveDay int64, changes []Change, now int64) error {
	return s.Execute(Op{Kind: OpAmend, ContractID: contractID, EffectiveDay: effectiveDay,
		Changes: changes, Now: now}).err()
}

// TerminateContract 终止合同。
func (s *Service) TerminateContract(contractID string, now int64) error {
	return s.Execute(Op{Kind: OpTerminate, ContractID: contractID, Now: now}).err()
}

// Summary 查询合同金额汇总。只读累计字段，O(1)，不触碰时钟与状态。
func (s *Service) Summary(contractID string) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contractID == "" {
		return Summary{}, invalidArg("合同 ID 为空")
	}
	c, ok := s.contracts[contractID]
	if !ok {
		return Summary{}, notFound("合同 %q 不存在", contractID)
	}
	return Summary{
		PaidTotal:              c.paidTotal,
		RetentionHeld:          c.retentionHeld,
		DefectForfeitTotal:     c.defectForfeit,
		AdvanceDeductedTotal:   c.advanceDeducted,
		PenaltyDeductedTotal:   c.penaltyDeducted,
		OutstandingPenaltyDebt: c.penaltyDebt,
		SettledAmountTotal:     c.settledAmount,
	}, nil
}

// MilestoneView 里程碑状态快照（审计与测试用）。
type MilestoneView struct {
	Amount      int64
	PlanDay     int64
	Accepted    bool
	PassDay     int64
	OverdueDays int64
	Settled     bool
	Released    bool
	Retention   int64
	OpenDefects int64
}

// InspectMilestone 读取里程碑状态快照。
func (s *Service) InspectMilestone(contractID, milestoneID string) (MilestoneView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, okc := s.contracts[contractID]
	if !okc {
		return MilestoneView{}, notFound("合同 %q 不存在", contractID)
	}
	m, okm := c.milestones[milestoneID]
	if !okm {
		return MilestoneView{}, notFound("里程碑 %q 不存在于合同 %q", milestoneID, contractID)
	}
	return MilestoneView{
		Amount:      m.amount,
		PlanDay:     m.planDay,
		Accepted:    m.accepted,
		PassDay:     m.passDay,
		OverdueDays: m.overdue,
		Settled:     m.settled,
		Released:    m.released,
		Retention:   m.retention,
		OpenDefects: m.openDefects,
	}, nil
}

// ContractIDs 返回全部合同 ID（排序后，保证确定性）。
func (s *Service) ContractIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.contracts))
	for id := range s.contracts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// mulDivFloor 精确计算 floor(a*b/d)，a、b 非负，d 为正。
// 使用大整数中间量，避免 int64 乘法溢出。
func mulDivFloor(a, b, d int64) int64 {
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	p.Quo(p, big.NewInt(d))
	return clampInt64(p)
}

// mulDivCeil 精确计算 ceil(a*b/d)，a、b 非负，d 为正。
func mulDivCeil(a, b, d int64) int64 {
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	q, r := new(big.Int).QuoRem(p, big.NewInt(d), new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return clampInt64(q)
}

// penaltyOf 精确计算 floor(overdueDays*dailyRate*totalAmount/10000)。
func penaltyOf(overdueDays, dailyRate, totalAmount int64) int64 {
	p := new(big.Int).Mul(big.NewInt(overdueDays), big.NewInt(dailyRate))
	p.Mul(p, big.NewInt(totalAmount))
	p.Quo(p, big.NewInt(ratioBase))
	return clampInt64(p)
}

func clampInt64(x *big.Int) int64 {
	if !x.IsInt64() {
		return math.MaxInt64
	}
	return x.Int64()
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
