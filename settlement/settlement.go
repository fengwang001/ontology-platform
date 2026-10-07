// Package settlement 实现采购合同履约付款结算服务。
//
// 金额一律以整数分计，时间一律以整数日序号计。
// 所有比例以万分比整数给出（10000 = 100%）。
// 服务的所有公开方法可并发调用，内部以互斥锁串行化，
// 结果等价于某个串行顺序；相同操作序列重放得到完全相同的结果。
package settlement

import "sync"

// basisPoints 是比例的分母（万分比）。
const basisPoints = 10000

// MilestoneParams 描述创建合同时的一个里程碑。
type MilestoneParams struct {
	ID      string
	Payable int64 // 应付金额（分）
	PlanDay int64 // 计划验收日（日序号）
}

// ContractParams 描述创建合同的全部固定条款。
type ContractParams struct {
	ID               string
	TotalAmount      int64 // 合同总金额（分）
	AdvanceTotal     int64 // 预付款总额（分），不可变更
	AdvanceRatio     int64 // 预付款抵扣比例（万分比）
	RetentionRatio   int64 // 质保金比例（万分比）
	RetentionDays    int64 // 质保期天数
	PenaltyDailyRate int64 // 违约金日费率（万分比，对合同总金额）
	PenaltyCapRatio  int64 // 违约金封顶比例（万分比，对合同总金额）
	Milestones       []MilestoneParams
}

// Adjustment 是变更单中对单个里程碑的调整。
type Adjustment struct {
	MilestoneID string
	Payable     int64
	PlanDay     int64
}

// SettlementResult 是一次里程碑验收通过后的结算结果。
type SettlementResult struct {
	MilestoneID   string
	Payable       int64 // 本次应付金额
	PassDay       int64 // 验收通过日
	PlanDay       int64 // 适用的计划验收日
	OverdueDays   int64 // 逾期天数（<=0 时为 0）
	Retention     int64 // 质保金扣留（向上取整）
	AdvanceDeduct int64 // 本次预付款抵扣（向下取整，受预付款总额约束）
	ArrearsPaid   int64 // 本次扣抵的历史违约金欠额
	PenaltyDue    int64 // 本次计提违约金（受累计封顶约束后）
	PenaltyPaid   int64 // 本次违约金实际扣抵（不含欠额扣抵）
	Paid          int64 // 本次实付（>=0）
	ArrearsLeft   int64 // 结算后仍未扣抵的违约金欠额
}

// ReleaseResult 是质保金释放的结果。
type ReleaseResult struct {
	MilestoneID  string
	Retention    int64 // 释放的质保金原额
	DefectDeduct int64 // 缺陷罚没扣抵（不超过质保金）
	Paid         int64 // 实际支付 = Retention - DefectDeduct
}

// Summary 是合同各类金额的守恒汇总。
type Summary struct {
	SettledPayableSum  int64 // 已结算里程碑的应付之和
	TotalPaid          int64 // 累计实付（结算实付 + 质保金释放支付）
	RetentionBalance   int64 // 质保金余额（扣留中、未释放）
	DefectDeducted     int64 // 缺陷罚没扣抵累计
	AdvanceDeducted    int64 // 预付款抵扣累计
	PenaltyDeducted    int64 // 违约金扣抵累计（含欠额扣抵）
	PenaltyCharged     int64 // 违约金累计计提 = PenaltyDeducted + ArrearsOutstanding
	ArrearsOutstanding int64 // 违约金欠额（不计入守恒之和）
	PenaltyCap         int64 // 违约金封顶额
	Terminated         bool
}

// Conserved 校验守恒恒等式：
// 已结算应付之和 == 累计实付 + 质保金余额 + 缺陷扣抵 + 预付款抵扣 + 违约金扣抵。
func (s Summary) Conserved() bool {
	return s.SettledPayableSum == s.TotalPaid+s.RetentionBalance+s.DefectDeducted+s.AdvanceDeducted+s.PenaltyDeducted
}

// milestoneVersion 是里程碑应付金额与计划日的一个版本。
type milestoneVersion struct {
	effectiveDay int64 // 生效日：验收通过日 >= 该日才适用
	payable      int64
	planDay      int64
}

// defect 是一条缺陷记录。
type defect struct {
	penalty int64
	open    bool
}

// milestone 是里程碑的内部状态。
type milestone struct {
	id string
	// base 为原始版本；pending 为已登记变更，按生效日升序（同生效日后登记者优先）。
	// 验收通过日 d 适用：生效日 <= d 的版本中最新的一个。
	base    milestoneVersion
	pending []milestoneVersion

	passed     bool
	rejections int64

	// 以下字段仅在验收通过后有意义。
	passDay   int64
	payable   int64
	planDay   int64
	retention int64
	released  bool

	defects          map[string]*defect
	openDefects      int64
	defectPenaltySum int64 // 已登记缺陷罚没金额之和（不论开闭）
}

// contract 是合同的内部状态，所有汇总字段均为 O(1) 增量维护的聚合值。
type contract struct {
	params ContractParams

	penaltyCap int64 // 违约金封顶额 = floor(TotalAmount * PenaltyCapRatio / 10000)

	milestones map[string]*milestone
	terminated bool

	// 守恒聚合（增量维护，查询 O(1)）。
	settledPayableSum int64
	paidSettle        int64
	paidRelease       int64
	retentionBalance  int64
	defectDeducted    int64
	advanceUsed       int64
	penaltyDeducted   int64
	penaltyCharged    int64 // = penaltyDeducted + arrears
	arrears           int64
}

// Service 是结算服务入口，须用 NewService 创建。
type Service struct {
	mu        sync.Mutex
	hasNow    bool
	lastNow   int64
	contracts map[string]*contract
}

// NewService 创建空的结算服务。
func NewService() *Service {
	return &Service{contracts: make(map[string]*contract)}
}

// CreateContract 创建合同。
func (s *Service) CreateContract(p ContractParams, now int64) error {
	const op = "CreateContract"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if err := validateContractParams(p); err != nil {
		return err
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return err
	}
	// 3. 状态不允许：合同 ID 已存在
	if _, ok := s.contracts[p.ID]; ok {
		return newError(ErrStateNotAllowed, op, "contract %q already exists", p.ID)
	}

	c := &contract{
		params:     p,
		penaltyCap: mulDivFloor(p.TotalAmount, p.PenaltyCapRatio),
		milestones: make(map[string]*milestone, len(p.Milestones)),
	}
	for _, mp := range p.Milestones {
		c.milestones[mp.ID] = &milestone{
			id:      mp.ID,
			base:    milestoneVersion{effectiveDay: now, payable: mp.Payable, planDay: mp.PlanDay},
			defects: make(map[string]*defect),
		}
	}
	s.contracts[p.ID] = c
	s.acceptClock(now)
	return nil
}

// Accept 里程碑验收；passed 为 true 时通过并立即结算，为 false 时驳回。
func (s *Service) Accept(contractID, milestoneID string, passed bool, now int64) (*SettlementResult, error) {
	const op = "Accept"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if contractID == "" || milestoneID == "" {
		return nil, newError(ErrInvalidParam, op, "empty contract or milestone id")
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return nil, err
	}
	// 3. 合同或里程碑不存在
	c, ok := s.contracts[contractID]
	if !ok {
		return nil, newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	m, ok := c.milestones[milestoneID]
	if !ok {
		return nil, newError(ErrNotFound, op, "milestone %q not found", milestoneID)
	}
	// 4. 状态不允许
	if c.terminated {
		return nil, newError(ErrStateNotAllowed, op, "contract %q terminated", contractID)
	}
	// 5. 重复结算
	if m.passed {
		return nil, newError(ErrDuplicateSettlement, op, "milestone %q already settled", milestoneID)
	}

	s.acceptClock(now)
	if !passed {
		// 驳回：不改变计划日，供应商可重新提交再验收。
		m.rejections++
		return nil, nil
	}

	// 应用验收日已生效的变更版本（每个待生效版本至多被消费一次）。
	for len(m.pending) > 0 && m.pending[0].effectiveDay <= now {
		m.base = m.pending[0]
		m.pending = m.pending[1:]
	}

	m.passed = true
	m.passDay = now
	m.payable = m.base.payable
	m.planDay = m.base.planDay

	res := c.settle(m, now)
	return &res, nil
}

// settle 按固定顺序对一笔应付金额 P 执行结算：
//  1. 质保金 R = ceil(P * 质保金比例 / 10000)，全额扣留；
//  2. 预付款抵扣 A = floor(P * 抵扣比例 / 10000)，累计不超过预付款总额，
//     且与前一项合计不超过 P（保证实付非负）；
//  3. 历史违约金欠额优先自剩余中扣抵；
//  4. 本次违约金 = floor(逾期天数 * 日费率 * 合同总金额 / 10000)，
//     累计计提（已扣抵 + 欠额）不超过封顶，剩余不足部分记为欠额结转。
func (c *contract) settle(m *milestone, now int64) SettlementResult {
	p := c.params
	payable := m.payable

	overdue := now - m.planDay
	if overdue < 0 {
		overdue = 0
	}

	retention := mulDivCeil(payable, p.RetentionRatio)

	advance := mulDivFloor(payable, p.AdvanceRatio)
	if remain := p.AdvanceTotal - c.advanceUsed; advance > remain {
		advance = remain
	}
	if maxDeduct := payable - retention; advance > maxDeduct {
		advance = maxDeduct
	}

	rem := payable - retention - advance

	// 欠额优先扣抵。
	arrearsPaid := min64(c.arrears, rem)
	rem -= arrearsPaid
	c.arrears -= arrearsPaid
	c.penaltyDeducted += arrearsPaid

	// 本次违约金计提，受累计封顶约束。
	due := penaltyAmount(overdue, p.PenaltyDailyRate, p.TotalAmount)
	if headroom := c.penaltyCap - c.penaltyCharged; due > headroom {
		due = headroom
	}
	penaltyPaid := min64(due, rem)
	rem -= penaltyPaid
	c.penaltyDeducted += penaltyPaid
	c.penaltyCharged += due
	c.arrears += due - penaltyPaid

	paid := rem

	// 质保金扣留与聚合更新。
	m.retention = retention
	c.retentionBalance += retention
	c.advanceUsed += advance
	c.settledPayableSum += payable
	c.paidSettle += paid

	return SettlementResult{
		MilestoneID:   m.id,
		Payable:       payable,
		PassDay:       now,
		PlanDay:       m.planDay,
		OverdueDays:   overdue,
		Retention:     retention,
		AdvanceDeduct: advance,
		ArrearsPaid:   arrearsPaid,
		PenaltyDue:    due,
		PenaltyPaid:   penaltyPaid,
		Paid:          paid,
		ArrearsLeft:   c.arrears,
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// validateContractParams 校验创建合同的参数（错误类别：参数非法）。
func validateContractParams(p ContractParams) error {
	const op = "CreateContract"
	if p.ID == "" {
		return newError(ErrInvalidParam, op, "empty contract id")
	}
	if p.TotalAmount <= 0 {
		return newError(ErrInvalidParam, op, "total amount must be positive, got %d", p.TotalAmount)
	}
	if p.AdvanceTotal < 0 {
		return newError(ErrInvalidParam, op, "advance total must be >= 0, got %d", p.AdvanceTotal)
	}
	for name, ratio := range map[string]int64{
		"advance ratio":      p.AdvanceRatio,
		"retention ratio":    p.RetentionRatio,
		"penalty daily rate": p.PenaltyDailyRate,
		"penalty cap ratio":  p.PenaltyCapRatio,
	} {
		if ratio < 0 || ratio > basisPoints {
			return newError(ErrInvalidParam, op, "%s must be in [0,10000], got %d", name, ratio)
		}
	}
	if p.RetentionDays < 0 {
		return newError(ErrInvalidParam, op, "retention days must be >= 0, got %d", p.RetentionDays)
	}
	seen := make(map[string]bool, len(p.Milestones))
	var sum int64
	for _, mp := range p.Milestones {
		if mp.ID == "" {
			return newError(ErrInvalidParam, op, "empty milestone id")
		}
		if seen[mp.ID] {
			return newError(ErrInvalidParam, op, "duplicate milestone id %q", mp.ID)
		}
		seen[mp.ID] = true
		if mp.Payable < 0 {
			return newError(ErrInvalidParam, op, "milestone %q payable must be >= 0, got %d", mp.ID, mp.Payable)
		}
		if mp.PlanDay < 0 {
			return newError(ErrInvalidParam, op, "milestone %q plan day must be >= 0, got %d", mp.ID, mp.PlanDay)
		}
		sum += mp.Payable
	}
	if sum > p.TotalAmount {
		return newError(ErrInvalidParam, op, "milestone payables sum %d exceeds total amount %d", sum, p.TotalAmount)
	}
	return nil
}

// checkClock 校验时钟回退（错误类别：时钟回退）。
func (s *Service) checkClock(op string, now int64) error {
	if s.hasNow && now < s.lastNow {
		return newError(ErrClockRollback, op, "now %d < last accepted now %d", now, s.lastNow)
	}
	return nil
}

// acceptClock 在操作被接受后推进时钟；被拒绝的操作不得改变时钟。
func (s *Service) acceptClock(now int64) {
	s.lastNow = now
	s.hasNow = true
}

// ReleaseRetention 释放指定里程碑的质保金。
//
// 质保期满日 = 验收通过日 + 质保期天数；满日当天不可释放，次日起可释放。
// 存在未决缺陷时暂停释放；释放时扣抵该里程碑已登记缺陷的罚没金额
// （合计不超过该质保金），不影响其他里程碑。
func (s *Service) ReleaseRetention(contractID, milestoneID string, now int64) (*ReleaseResult, error) {
	const op = "ReleaseRetention"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if contractID == "" || milestoneID == "" {
		return nil, newError(ErrInvalidParam, op, "empty contract or milestone id")
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return nil, err
	}
	// 3. 合同或里程碑不存在
	c, ok := s.contracts[contractID]
	if !ok {
		return nil, newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	m, ok := c.milestones[milestoneID]
	if !ok {
		return nil, newError(ErrNotFound, op, "milestone %q not found", milestoneID)
	}
	// 4. 状态不允许
	if !m.passed {
		return nil, newError(ErrStateNotAllowed, op, "milestone %q not accepted yet", milestoneID)
	}
	if m.released {
		return nil, newError(ErrStateNotAllowed, op, "milestone %q retention already released", milestoneID)
	}
	expiry := m.passDay + c.params.RetentionDays
	if now <= expiry {
		return nil, newError(ErrStateNotAllowed, op, "retention period expires day %d, releasable from day %d, now %d", expiry, expiry+1, now)
	}
	if m.openDefects > 0 {
		return nil, newError(ErrStateNotAllowed, op, "milestone %q has %d open defect(s), release paused", milestoneID, m.openDefects)
	}

	s.acceptClock(now)

	defectDeduct := min64(m.defectPenaltySum, m.retention)
	paid := m.retention - defectDeduct

	m.released = true
	c.retentionBalance -= m.retention
	c.defectDeducted += defectDeduct
	c.paidRelease += paid

	return &ReleaseResult{
		MilestoneID:  milestoneID,
		Retention:    m.retention,
		DefectDeduct: defectDeduct,
		Paid:         paid,
	}, nil
}

// RegisterDefect 对单个里程碑登记缺陷及罚没金额。
// 缺陷只暂停该里程碑的质保金释放；合同终止后仍可登记（质保规则不变）。
func (s *Service) RegisterDefect(contractID, milestoneID, defectID string, penalty int64, now int64) error {
	const op = "RegisterDefect"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if contractID == "" || milestoneID == "" || defectID == "" {
		return newError(ErrInvalidParam, op, "empty contract, milestone or defect id")
	}
	if penalty < 0 {
		return newError(ErrInvalidParam, op, "defect penalty must be >= 0, got %d", penalty)
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return err
	}
	// 3. 合同或里程碑不存在
	c, ok := s.contracts[contractID]
	if !ok {
		return newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	m, ok := c.milestones[milestoneID]
	if !ok {
		return newError(ErrNotFound, op, "milestone %q not found", milestoneID)
	}
	// 4. 状态不允许
	if !m.passed {
		return newError(ErrStateNotAllowed, op, "milestone %q not accepted yet", milestoneID)
	}
	if m.released {
		return newError(ErrStateNotAllowed, op, "milestone %q retention already released", milestoneID)
	}
	if _, dup := m.defects[defectID]; dup {
		return newError(ErrStateNotAllowed, op, "defect %q already registered", defectID)
	}

	s.acceptClock(now)
	m.defects[defectID] = &defect{penalty: penalty, open: true}
	m.openDefects++
	m.defectPenaltySum += penalty
	return nil
}

// CloseDefect 关闭缺陷。
func (s *Service) CloseDefect(contractID, milestoneID, defectID string, now int64) error {
	const op = "CloseDefect"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if contractID == "" || milestoneID == "" || defectID == "" {
		return newError(ErrInvalidParam, op, "empty contract, milestone or defect id")
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return err
	}
	// 3. 合同、里程碑或缺陷不存在
	c, ok := s.contracts[contractID]
	if !ok {
		return newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	m, ok := c.milestones[milestoneID]
	if !ok {
		return newError(ErrNotFound, op, "milestone %q not found", milestoneID)
	}
	d, ok := m.defects[defectID]
	if !ok {
		return newError(ErrNotFound, op, "defect %q not found", defectID)
	}
	// 4. 状态不允许
	if !d.open {
		return newError(ErrStateNotAllowed, op, "defect %q already closed", defectID)
	}

	s.acceptClock(now)
	d.open = false
	m.openDefects--
	return nil
}

// ChangeOrder 提交合同变更单。
//
// 仅可调整尚未验收通过的里程碑；变更自生效日起对之后验收的里程碑适用，
// 已验收通过或已结算的里程碑不追溯。同一里程碑可登记多个变更，
// 验收时适用生效日不晚于验收日的最新版本（同生效日后登记者优先）。
// 变更后各里程碑应付金额之和不得超过合同总金额。预付款总额不可变更。
func (s *Service) ChangeOrder(contractID string, effectiveDay int64, adjs []Adjustment, now int64) error {
	const op = "ChangeOrder"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if contractID == "" {
		return newError(ErrInvalidParam, op, "empty contract id")
	}
	if len(adjs) == 0 {
		return newError(ErrInvalidParam, op, "empty adjustments")
	}
	if effectiveDay < now {
		return newError(ErrInvalidParam, op, "effective day %d < now %d", effectiveDay, now)
	}
	seen := make(map[string]bool, len(adjs))
	for _, a := range adjs {
		if a.MilestoneID == "" {
			return newError(ErrInvalidParam, op, "empty milestone id in adjustment")
		}
		if seen[a.MilestoneID] {
			return newError(ErrInvalidParam, op, "duplicate adjustment for milestone %q", a.MilestoneID)
		}
		seen[a.MilestoneID] = true
		if a.Payable < 0 {
			return newError(ErrInvalidParam, op, "milestone %q payable must be >= 0, got %d", a.MilestoneID, a.Payable)
		}
		if a.PlanDay < 0 {
			return newError(ErrInvalidParam, op, "milestone %q plan day must be >= 0, got %d", a.MilestoneID, a.PlanDay)
		}
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return err
	}
	// 3. 合同或里程碑不存在
	c, ok := s.contracts[contractID]
	if !ok {
		return newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	for _, a := range adjs {
		if _, ok := c.milestones[a.MilestoneID]; !ok {
			return newError(ErrNotFound, op, "milestone %q not found", a.MilestoneID)
		}
	}
	// 4. 状态不允许
	if c.terminated {
		return newError(ErrStateNotAllowed, op, "contract %q terminated", contractID)
	}
	for _, a := range adjs {
		if c.milestones[a.MilestoneID].passed {
			return newError(ErrStateNotAllowed, op, "milestone %q already accepted, change not retroactive", a.MilestoneID)
		}
	}
	// 6. 变更超出合同总额（以各里程碑最终预定值计算）
	var sum int64
	for _, m := range c.milestones {
		v := m.finalValue()
		if adj, ok := findAdj(adjs, m.id); ok {
			v.payable = adj.Payable
		}
		sum += v.payable
	}
	if sum > c.params.TotalAmount {
		return newError(ErrChangeExceedsTotal, op, "milestone payables sum %d exceeds total amount %d", sum, c.params.TotalAmount)
	}

	s.acceptClock(now)
	for _, a := range adjs {
		m := c.milestones[a.MilestoneID]
		m.addPending(milestoneVersion{effectiveDay: effectiveDay, payable: a.Payable, planDay: a.PlanDay})
	}
	return nil
}

// finalValue 返回该里程碑最终（最远生效日）的预定版本。
func (m *milestone) finalValue() milestoneVersion {
	if len(m.pending) > 0 {
		return m.pending[len(m.pending)-1]
	}
	return m.base
}

// addPending 按生效日升序插入变更版本；同生效日的旧版本被后登记者替换。
func (m *milestone) addPending(v milestoneVersion) {
	i := 0
	for i < len(m.pending) && m.pending[i].effectiveDay < v.effectiveDay {
		i++
	}
	if i < len(m.pending) && m.pending[i].effectiveDay == v.effectiveDay {
		m.pending[i] = v
		return
	}
	m.pending = append(m.pending, milestoneVersion{})
	copy(m.pending[i+1:], m.pending[i:])
	m.pending[i] = v
}

func findAdj(adjs []Adjustment, id string) (Adjustment, bool) {
	for _, a := range adjs {
		if a.MilestoneID == id {
			return a, true
		}
	}
	return Adjustment{}, false
}

// Terminate 终止合同。
//
// 已结算的保持不变；未结算的里程碑不再产生应付款；
// 已扣留的质保金仍按原规则释放；违约金欠额不再扣抵。
func (s *Service) Terminate(contractID string, now int64) error {
	const op = "Terminate"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if contractID == "" {
		return newError(ErrInvalidParam, op, "empty contract id")
	}
	// 2. 时钟回退
	if err := s.checkClock(op, now); err != nil {
		return err
	}
	// 3. 合同不存在
	c, ok := s.contracts[contractID]
	if !ok {
		return newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	// 4. 状态不允许
	if c.terminated {
		return newError(ErrStateNotAllowed, op, "contract %q already terminated", contractID)
	}

	s.acceptClock(now)
	c.terminated = true
	return nil
}

// Summary 返回合同汇总；只读操作，不校验也不推进时钟。
func (s *Service) Summary(contractID string) (Summary, error) {
	const op = "Summary"
	s.mu.Lock()
	defer s.mu.Unlock()

	if contractID == "" {
		return Summary{}, newError(ErrInvalidParam, op, "empty contract id")
	}
	c, ok := s.contracts[contractID]
	if !ok {
		return Summary{}, newError(ErrNotFound, op, "contract %q not found", contractID)
	}
	return Summary{
		SettledPayableSum:  c.settledPayableSum,
		TotalPaid:          c.paidSettle + c.paidRelease,
		RetentionBalance:   c.retentionBalance,
		DefectDeducted:     c.defectDeducted,
		AdvanceDeducted:    c.advanceUsed,
		PenaltyDeducted:    c.penaltyDeducted,
		PenaltyCharged:     c.penaltyCharged,
		ArrearsOutstanding: c.arrears,
		PenaltyCap:         c.penaltyCap,
		Terminated:         c.terminated,
	}, nil
}
