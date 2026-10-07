package settlement_test

// 本文件包含一个独立编写的朴素模型，用于与 settlement.Service 对照。
// 朴素模型保留全部历史记录，每次查询都遍历历史重算（O(n)），
// 与被测服务的 O(1) 增量聚合互为实现参照。
// 注意：模型使用普通 int64 算术，仅对测试值域（金额 <= 1e9）有效。

import (
	"fmt"

	"ontology/settlement"
)

// mVersion 是模型中里程碑应付/计划日的一个版本。
type mVersion struct {
	eff     int64
	payable int64
	planDay int64
}

// mDefect 是模型中的一条缺陷记录。
type mDefect struct {
	penalty int64
	open    bool
}

// mMilestone 是模型中的里程碑，保留全部版本历史。
type mMilestone struct {
	versions  []mVersion // 登记顺序保存
	passed    bool
	passDay   int64
	payable   int64
	planDay   int64
	retention int64
	released  bool
	defects   map[string]*mDefect
}

// valueAt 返回验收日 d 适用的版本：生效日 <= d 中生效日最大者（同日后登记者优先）。
func (m *mMilestone) valueAt(d int64) mVersion {
	best := 0
	for i, v := range m.versions {
		if v.eff <= d && v.eff >= m.versions[best].eff {
			best = i
		}
	}
	return m.versions[best]
}

// finalValue 返回最终（生效日最大，同日后登记者优先）的预定版本。
func (m *mMilestone) finalValue() mVersion {
	best := 0
	for i, v := range m.versions {
		if v.eff >= m.versions[best].eff {
			best = i
		}
	}
	return m.versions[best]
}

// mSettlement 是模型保存的一次结算历史。
type mSettlement struct {
	payable, retention, advance   int64
	arrearsPaid, due, penaltyPaid int64
	paid                          int64
}

// mRelease 是模型保存的一次质保金释放历史。
type mRelease struct {
	retention, defectDeduct, paid int64
}

// mContract 是模型中的合同。
type mContract struct {
	p           settlement.ContractParams
	cap         int64
	milestones  map[string]*mMilestone
	terminated  bool
	settlements []mSettlement
	releases    []mRelease
}

// model 是朴素模型服务。
type model struct {
	hasNow    bool
	lastNow   int64
	contracts map[string]*mContract
}

func newModel() *model {
	return &model{contracts: make(map[string]*mContract)}
}

func merr(kind settlement.ErrorKind, format string, args ...any) *settlement.Error {
	return &settlement.Error{Kind: kind, Op: "model", Msg: fmt.Sprintf(format, args...)}
}

func (md *model) clock(now int64) *settlement.Error {
	if md.hasNow && now < md.lastNow {
		return merr(settlement.ErrClockRollback, "now %d < last accepted now %d", now, md.lastNow)
	}
	return nil
}

func (md *model) acceptClock(now int64) {
	md.lastNow, md.hasNow = now, true
}

func (md *model) createContract(p settlement.ContractParams, now int64) *settlement.Error {
	if err := md.clock(now); err != nil {
		return err
	}
	if _, ok := md.contracts[p.ID]; ok {
		return merr(settlement.ErrStateNotAllowed, "contract %q already exists", p.ID)
	}
	c := &mContract{
		p:          p,
		cap:        p.TotalAmount * p.PenaltyCapRatio / 10000,
		milestones: make(map[string]*mMilestone),
	}
	for _, mp := range p.Milestones {
		c.milestones[mp.ID] = &mMilestone{
			versions: []mVersion{{eff: now, payable: mp.Payable, planDay: mp.PlanDay}},
			defects:  make(map[string]*mDefect),
		}
	}
	md.contracts[p.ID] = c
	md.acceptClock(now)
	return nil
}

// advanceUsed 遍历结算历史重算预付款抵扣累计。
func (c *mContract) advanceUsed() int64 {
	var s int64
	for _, st := range c.settlements {
		s += st.advance
	}
	return s
}

// penaltyCharged 遍历结算历史重算违约金累计计提。
func (c *mContract) penaltyCharged() int64 {
	var s int64
	for _, st := range c.settlements {
		s += st.due
	}
	return s
}

// arrears 遍历结算历史重算违约金欠额。
func (c *mContract) arrears() int64 {
	var charged, deducted int64
	for _, st := range c.settlements {
		charged += st.due
		deducted += st.arrearsPaid + st.penaltyPaid
	}
	return charged - deducted
}

func (md *model) accept(cid, mid string, passed bool, now int64) (*settlement.SettlementResult, *settlement.Error, string) {
	if cid == "" || mid == "" {
		return nil, merr(settlement.ErrInvalidParam, "empty id"), "参数非法：空 ID"
	}
	if err := md.clock(now); err != nil {
		return nil, err, "时钟回退"
	}
	c, ok := md.contracts[cid]
	if !ok {
		return nil, merr(settlement.ErrNotFound, "contract %q not found", cid), "合同不存在"
	}
	m, ok := c.milestones[mid]
	if !ok {
		return nil, merr(settlement.ErrNotFound, "milestone %q not found", mid), "里程碑不存在"
	}
	if c.terminated {
		return nil, merr(settlement.ErrStateNotAllowed, "terminated"), "合同已终止"
	}
	if m.passed {
		return nil, merr(settlement.ErrDuplicateSettlement, "already settled"), "重复结算"
	}
	md.acceptClock(now)
	if !passed {
		return nil, nil, "驳回：计划日不变，可重新提交"
	}

	v := m.valueAt(now)
	m.passed = true
	m.passDay = now
	m.payable = v.payable
	m.planDay = v.planDay
	m.retention = (v.payable*c.p.RetentionRatio + 9999) / 10000

	overdue := now - v.planDay
	if overdue < 0 {
		overdue = 0
	}
	retention := m.retention
	advance := v.payable * c.p.AdvanceRatio / 10000
	if rem := c.p.AdvanceTotal - c.advanceUsed(); advance > rem {
		advance = rem
	}
	if maxDeduct := v.payable - retention; advance > maxDeduct {
		advance = maxDeduct
	}
	rem := v.payable - retention - advance

	arrearsPaid := c.arrears()
	if arrearsPaid > rem {
		arrearsPaid = rem
	}
	rem -= arrearsPaid

	due := overdue * c.p.PenaltyDailyRate * c.p.TotalAmount / 10000
	if headroom := c.cap - c.penaltyCharged(); due > headroom {
		due = headroom
	}
	penaltyPaid := due
	if penaltyPaid > rem {
		penaltyPaid = rem
	}
	rem -= penaltyPaid
	paid := rem

	c.settlements = append(c.settlements, mSettlement{
		payable: v.payable, retention: retention, advance: advance,
		arrearsPaid: arrearsPaid, due: due, penaltyPaid: penaltyPaid, paid: paid,
	})

	res := &settlement.SettlementResult{
		MilestoneID:   mid,
		Payable:       v.payable,
		PassDay:       now,
		PlanDay:       v.planDay,
		OverdueDays:   overdue,
		Retention:     retention,
		AdvanceDeduct: advance,
		ArrearsPaid:   arrearsPaid,
		PenaltyDue:    due,
		PenaltyPaid:   penaltyPaid,
		Paid:          paid,
		ArrearsLeft:   c.arrears(),
	}
	note := fmt.Sprintf("结算: P=%d R=ceil=%d A=floor=%d 余=%d 欠额扣抵=%d 本次违约金=%d 实付=%d",
		v.payable, retention, advance, v.payable-retention-advance, arrearsPaid, due, paid)
	return res, nil, note
}

func (md *model) release(cid, mid string, now int64) (*settlement.ReleaseResult, *settlement.Error, string) {
	if cid == "" || mid == "" {
		return nil, merr(settlement.ErrInvalidParam, "empty id"), "参数非法：空 ID"
	}
	if err := md.clock(now); err != nil {
		return nil, err, "时钟回退"
	}
	c, ok := md.contracts[cid]
	if !ok {
		return nil, merr(settlement.ErrNotFound, "contract %q not found", cid), "合同不存在"
	}
	m, ok := c.milestones[mid]
	if !ok {
		return nil, merr(settlement.ErrNotFound, "milestone %q not found", mid), "里程碑不存在"
	}
	if !m.passed {
		return nil, merr(settlement.ErrStateNotAllowed, "not accepted"), "未验收通过"
	}
	if m.released {
		return nil, merr(settlement.ErrStateNotAllowed, "already released"), "质保金已释放"
	}
	expiry := m.passDay + c.p.RetentionDays
	if now <= expiry {
		return nil, merr(settlement.ErrStateNotAllowed, "not expired"),
			fmt.Sprintf("质保期未满：满日 %d，次日 %d 起可释放，now=%d", expiry, expiry+1, now)
	}
	var open int64
	var penaltySum int64
	for _, d := range m.defects {
		if d.open {
			open++
		}
		penaltySum += d.penalty
	}
	if open > 0 {
		return nil, merr(settlement.ErrStateNotAllowed, "open defects"),
			fmt.Sprintf("存在 %d 个未决缺陷，暂停释放", open)
	}
	md.acceptClock(now)

	// 该里程碑的质保金：通过日结算时记录的值。
	mr := m.retention
	deduct := penaltySum
	if deduct > mr {
		deduct = mr
	}
	paid := mr - deduct
	m.released = true
	c.releases = append(c.releases, mRelease{retention: mr, defectDeduct: deduct, paid: paid})
	res := &settlement.ReleaseResult{MilestoneID: mid, Retention: mr, DefectDeduct: deduct, Paid: paid}
	note := fmt.Sprintf("释放: 质保金=%d 缺陷扣抵=%d 实付=%d", mr, deduct, paid)
	return res, nil, note
}

func (md *model) registerDefect(cid, mid, did string, penalty int64, now int64) (*settlement.Error, string) {
	if cid == "" || mid == "" || did == "" {
		return merr(settlement.ErrInvalidParam, "empty id"), "参数非法：空 ID"
	}
	if penalty < 0 {
		return merr(settlement.ErrInvalidParam, "negative penalty"), "参数非法：罚没金额为负"
	}
	if err := md.clock(now); err != nil {
		return err, "时钟回退"
	}
	c, ok := md.contracts[cid]
	if !ok {
		return merr(settlement.ErrNotFound, "contract %q not found", cid), "合同不存在"
	}
	m, ok := c.milestones[mid]
	if !ok {
		return merr(settlement.ErrNotFound, "milestone %q not found", mid), "里程碑不存在"
	}
	if !m.passed {
		return merr(settlement.ErrStateNotAllowed, "not accepted"), "未验收通过"
	}
	if m.released {
		return merr(settlement.ErrStateNotAllowed, "already released"), "质保金已释放"
	}
	if _, dup := m.defects[did]; dup {
		return merr(settlement.ErrStateNotAllowed, "duplicate defect"), "缺陷已登记"
	}
	md.acceptClock(now)
	m.defects[did] = &mDefect{penalty: penalty, open: true}
	return nil, fmt.Sprintf("登记缺陷 %s 罚没 %d", did, penalty)
}

func (md *model) closeDefect(cid, mid, did string, now int64) (*settlement.Error, string) {
	if cid == "" || mid == "" || did == "" {
		return merr(settlement.ErrInvalidParam, "empty id"), "参数非法：空 ID"
	}
	if err := md.clock(now); err != nil {
		return err, "时钟回退"
	}
	c, ok := md.contracts[cid]
	if !ok {
		return merr(settlement.ErrNotFound, "contract %q not found", cid), "合同不存在"
	}
	m, ok := c.milestones[mid]
	if !ok {
		return merr(settlement.ErrNotFound, "milestone %q not found", mid), "里程碑不存在"
	}
	d, ok := m.defects[did]
	if !ok {
		return merr(settlement.ErrNotFound, "defect %q not found", did), "缺陷不存在"
	}
	if !d.open {
		return merr(settlement.ErrStateNotAllowed, "already closed"), "缺陷已关闭"
	}
	md.acceptClock(now)
	d.open = false
	return nil, fmt.Sprintf("关闭缺陷 %s", did)
}

func (md *model) changeOrder(cid string, eff int64, adjs []settlement.Adjustment, now int64) (*settlement.Error, string) {
	if cid == "" {
		return merr(settlement.ErrInvalidParam, "empty id"), "参数非法：空 ID"
	}
	if len(adjs) == 0 {
		return merr(settlement.ErrInvalidParam, "empty adjustments"), "参数非法：空变更"
	}
	if eff < now {
		return merr(settlement.ErrInvalidParam, "effective day in past"), "参数非法：生效日早于 now"
	}
	seen := map[string]bool{}
	for _, a := range adjs {
		if a.MilestoneID == "" || a.Payable < 0 || a.PlanDay < 0 || seen[a.MilestoneID] {
			return merr(settlement.ErrInvalidParam, "bad adjustment"), "参数非法：变更项非法"
		}
		seen[a.MilestoneID] = true
	}
	if err := md.clock(now); err != nil {
		return err, "时钟回退"
	}
	c, ok := md.contracts[cid]
	if !ok {
		return merr(settlement.ErrNotFound, "contract %q not found", cid), "合同不存在"
	}
	for _, a := range adjs {
		if _, ok := c.milestones[a.MilestoneID]; !ok {
			return merr(settlement.ErrNotFound, "milestone %q not found", a.MilestoneID), "里程碑不存在"
		}
	}
	if c.terminated {
		return merr(settlement.ErrStateNotAllowed, "terminated"), "合同已终止"
	}
	for _, a := range adjs {
		if c.milestones[a.MilestoneID].passed {
			return merr(settlement.ErrStateNotAllowed, "already accepted"), "已验收通过，变更不追溯"
		}
	}
	var sum int64
	for id, m := range c.milestones {
		v := m.finalValue()
		for _, a := range adjs {
			if a.MilestoneID == id {
				v.payable = a.Payable
			}
		}
		sum += v.payable
	}
	if sum > c.p.TotalAmount {
		return merr(settlement.ErrChangeExceedsTotal, "sum %d > total %d", sum, c.p.TotalAmount),
			fmt.Sprintf("变更超出合同总额：%d > %d", sum, c.p.TotalAmount)
	}
	md.acceptClock(now)
	for _, a := range adjs {
		m := c.milestones[a.MilestoneID]
		m.versions = append(m.versions, mVersion{eff: eff, payable: a.Payable, planDay: a.PlanDay})
	}
	return nil, fmt.Sprintf("变更生效日 %d，调整 %d 项", eff, len(adjs))
}

func (md *model) terminate(cid string, now int64) (*settlement.Error, string) {
	if cid == "" {
		return merr(settlement.ErrInvalidParam, "empty id"), "参数非法：空 ID"
	}
	if err := md.clock(now); err != nil {
		return err, "时钟回退"
	}
	c, ok := md.contracts[cid]
	if !ok {
		return merr(settlement.ErrNotFound, "contract %q not found", cid), "合同不存在"
	}
	if c.terminated {
		return merr(settlement.ErrStateNotAllowed, "already terminated"), "合同已终止"
	}
	md.acceptClock(now)
	c.terminated = true
	return nil, "终止合同"
}

// summary 遍历全部历史重算汇总（O(n)，与被测服务的 O(1) 聚合对照）。
func (md *model) summary(cid string) (settlement.Summary, *settlement.Error) {
	c, ok := md.contracts[cid]
	if !ok {
		return settlement.Summary{}, merr(settlement.ErrNotFound, "contract %q not found", cid)
	}
	var s settlement.Summary
	for _, st := range c.settlements {
		s.SettledPayableSum += st.payable
		s.TotalPaid += st.paid
		s.RetentionBalance += st.retention
		s.AdvanceDeducted += st.advance
		s.PenaltyDeducted += st.arrearsPaid + st.penaltyPaid
		s.PenaltyCharged += st.due
	}
	for _, r := range c.releases {
		s.RetentionBalance -= r.retention
		s.DefectDeducted += r.defectDeduct
		s.TotalPaid += r.paid
	}
	s.ArrearsOutstanding = s.PenaltyCharged - s.PenaltyDeducted
	s.PenaltyCap = c.cap
	s.Terminated = c.terminated
	return s, nil
}
