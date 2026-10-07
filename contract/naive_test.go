package contract

// 朴素模型：与被测实现完全独立编写的参照物。
// 不用任何增量结构：每次查询都按操作序列全量扫描、重新推演，
// 用于与优化实现（物化时间线 + 惰性续签游标）做差分对照。

import "math"

type naiveAmendment struct {
	declared     int
	mods         map[int]int
	revokeTarget int // -1 表示修改类协议

	signs       map[string]int
	firstSign   int
	complete    bool
	completeDay int
	completeSeq int64

	needsCountersign bool
	countersigned    bool
	countersignDay   int
}

func (a *naiveAmendment) effective() bool {
	return a.complete && a.countersigned
}

func (a *naiveAmendment) effDay() int {
	d := max(a.declared, a.completeDay)
	if a.needsCountersign {
		d = max(d, a.countersignDay)
	}
	return d
}

type naiveNotice struct {
	day   int
	party string
}

type naiveContract struct {
	p          ContractParams
	amendments []*naiveAmendment
	notices    []naiveNotice
	terminated bool
	termDay    int
}

type naive struct {
	lastNow   int
	seq       int64
	contracts map[int]*naiveContract
}

func newNaive() *naive {
	return &naive{contracts: map[int]*naiveContract{}}
}

// naiveRevoked 全量扫描撤销链：协议 i 在 day 当日是否被撤销。
func (c *naiveContract) revoked(i, day int) bool {
	for j, r := range c.amendments {
		if r.revokeTarget != i || !r.effective() {
			continue
		}
		eff := r.effDay()
		if eff > day {
			continue
		}
		if c.terminated && eff > c.termDay {
			continue
		}
		if !c.revoked(j, day) {
			return true
		}
	}
	return false
}

// valueAt 全量扫描所有协议，返回条款在 day 的有效值、来源与判定依据。
func (c *naiveContract) valueAt(clause, day int) (int, int, string) {
	best := -1
	bestEff, bestSeq := 0, int64(-1)
	bestVal := 0
	for i, a := range c.amendments {
		if !a.effective() {
			continue
		}
		eff := a.effDay()
		if eff > day {
			continue
		}
		if c.terminated && eff > c.termDay {
			continue
		}
		v, ok := a.mods[clause]
		if !ok {
			continue
		}
		if c.revoked(i, day) {
			continue
		}
		if best == -1 || eff > bestEff || (eff == bestEff && a.completeSeq > bestSeq) {
			best, bestEff, bestSeq, bestVal = i, eff, a.completeSeq, v
		}
	}
	if best == -1 {
		return c.p.Clauses[clause], -1, "无生效修改，取主合同原值"
	}
	return bestVal, best, ""
}

func (c *naiveContract) hasTimelyNotice(E, n, boundary int) bool {
	for _, x := range c.notices {
		if x.day > boundary && x.day <= E-n {
			return true
		}
	}
	return false
}

// inForce 逐任期重推演，报告 day 当日是否在期。
func (c *naiveContract) inForce(day int) bool {
	if day < c.p.Start {
		return false
	}
	E := c.p.Expiry
	boundary := math.MinInt
	for {
		if c.terminated && c.termDay <= E {
			return day <= c.termDay
		}
		if day <= E {
			return true
		}
		auto, _, _ := c.valueAt(c.p.AutoRenewClause, E)
		period, _, _ := c.valueAt(c.p.PeriodClause, E)
		noticeDays, _, _ := c.valueAt(c.p.NoticeClause, E)
		if auto == 0 || period <= 0 || c.hasTimelyNotice(E, noticeDays, boundary) {
			return false // day > E 且不再续签
		}
		boundary = E
		E += period
	}
}

// renewalsUpTo 重推演截至 now（不含）已发生的全部续签。
func (c *naiveContract) renewalsUpTo(now int) []Renewal {
	var out []Renewal
	E := c.p.Expiry
	boundary := math.MinInt
	for E < now {
		if c.terminated && c.termDay <= E {
			break
		}
		auto, _, _ := c.valueAt(c.p.AutoRenewClause, E)
		period, _, _ := c.valueAt(c.p.PeriodClause, E)
		noticeDays, _, _ := c.valueAt(c.p.NoticeClause, E)
		if auto == 0 || period <= 0 || c.hasTimelyNotice(E, noticeDays, boundary) {
			break
		}
		out = append(out, Renewal{OldExpiry: E, NewExpiry: E + period, Period: period, NoticeDays: noticeDays})
		boundary = E
		E += period
	}
	return out
}

// currentExpiry 重推演当前到期日（已终止/届满时返回最后在期日）。
func (c *naiveContract) currentExpiry(now int) int {
	E := c.p.Expiry
	boundary := math.MinInt
	for E < now {
		if c.terminated && c.termDay <= E {
			return c.termDay
		}
		auto, _, _ := c.valueAt(c.p.AutoRenewClause, E)
		period, _, _ := c.valueAt(c.p.PeriodClause, E)
		noticeDays, _, _ := c.valueAt(c.p.NoticeClause, E)
		if auto == 0 || period <= 0 || c.hasTimelyNotice(E, noticeDays, boundary) {
			return E
		}
		boundary = E
		E += period
	}
	if c.terminated && c.termDay < E {
		return c.termDay
	}
	return E
}

// --- 朴素模型的操作入口：与正式实现相同的校验顺序与错误类别 ---

func nfail(cat Category) OpResult { return OpResult{Err: cat} }

func (n *naive) checkClock(now int) (Category, bool) {
	if now < 0 {
		return InvalidParam, false
	}
	if now < n.lastNow {
		return ClockRollback, false
	}
	return None, true
}

func (n *naive) accept(now int) {
	n.lastNow = now
	n.seq++
}

func (n *naive) partyIndex(c *naiveContract, party string) int {
	for i, p := range c.p.Parties {
		if p == party {
			return i
		}
	}
	return -1
}

// apply 执行操作并返回结果与判定依据（用于日志）。
func (n *naive) apply(op Op) (OpResult, string) {
	switch op.Kind {
	case OpCreateContract:
		return n.createContract(op)
	case OpCreateAmendment:
		return n.createAmendment(op)
	case OpSign:
		return n.sign(op)
	case OpCountersign:
		return n.countersign(op)
	case OpNotice:
		return n.notice(op)
	case OpTerminate:
		return n.terminate(op)
	case OpQueryValue:
		return n.queryValue(op)
	case OpQueryInForce:
		return n.queryInForce(op)
	case OpQueryExpiry:
		return n.queryExpiry(op)
	case OpQueryRenewals:
		return n.queryRenewals(op)
	}
	return nfail(InvalidParam), "未知操作类别"
}

func (n *naive) createContract(op Op) (OpResult, string) {
	p := op.Params
	if p.Parties[0] == "" || p.Parties[1] == "" || p.Parties[0] == p.Parties[1] ||
		len(p.Clauses) == 0 || p.Start < 0 || p.Expiry < p.Start || p.SignWindowDays < 0 {
		return nfail(InvalidParam), "合同参数非法"
	}
	okRenewal := true
	for _, k := range []int{p.AutoRenewClause, p.PeriodClause, p.NoticeClause} {
		if _, ok := p.Clauses[k]; !ok {
			okRenewal = false
		}
	}
	okLocked := true
	for _, k := range p.Locked {
		if _, ok := p.Clauses[k]; !ok {
			okLocked = false
		}
	}
	if !okRenewal || !okLocked {
		return nfail(InvalidParam), "条款编号不在条款集合内"
	}
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	id := len(n.contracts)
	n.contracts[id] = &naiveContract{p: p}
	n.accept(op.Now)
	return OpResult{ContractID: id}, "合同创建成功"
}

func (n *naive) getContract(id int) *naiveContract { return n.contracts[id] }

func (n *naive) active(c *naiveContract, now int) bool {
	if c.terminated {
		return false
	}
	return c.inForce(now)
}

func (n *naive) createAmendment(op Op) (OpResult, string) {
	if op.DeclaredEffDay < 0 {
		return nfail(InvalidParam), "声明生效日为负"
	}
	if op.IsRevocation {
		if len(op.Mods) != 0 || op.RevokeTarget < 0 {
			return nfail(InvalidParam), "撤销协议参数非法"
		}
	} else if len(op.Mods) == 0 {
		return nfail(InvalidParam), "修改类协议至少修改一条条款"
	}
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	var target *naiveAmendment
	if op.IsRevocation {
		if op.RevokeTarget >= len(c.amendments) {
			return nfail(NotFound), "被撤销协议不存在"
		}
		target = c.amendments[op.RevokeTarget]
	} else {
		for clause := range op.Mods {
			if _, ok := c.p.Clauses[clause]; !ok {
				return nfail(InvalidParam), "条款不在合同内"
			}
		}
	}
	if !n.active(c, op.Now) {
		return nfail(StateNotAllowed), "合同不在可变更状态"
	}
	if op.IsRevocation {
		if !target.complete {
			return nfail(StateNotAllowed), "目标协议尚未签完"
		}
		if target.needsCountersign && !target.countersigned {
			return nfail(CountersignMissing), "目标协议缺少法务会签"
		}
		if !target.effective() || target.effDay() > op.Now {
			return nfail(StateNotAllowed), "目标协议尚未生效"
		}
	}
	a := &naiveAmendment{
		declared:     op.DeclaredEffDay,
		revokeTarget: -1,
		signs:        map[string]int{},
		firstSign:    -1,
	}
	if op.IsRevocation {
		a.revokeTarget = op.RevokeTarget
		a.countersigned = true
	} else {
		a.mods = map[int]int{}
		for k, v := range op.Mods {
			a.mods[k] = v
		}
		for k := range op.Mods {
			for _, l := range c.p.Locked {
				if k == l {
					a.needsCountersign = true
				}
			}
		}
		a.countersigned = !a.needsCountersign
	}
	c.amendments = append(c.amendments, a)
	n.accept(op.Now)
	return OpResult{AmendmentID: len(c.amendments) - 1}, "协议创建成功"
}

func (n *naive) sign(op Op) (OpResult, string) {
	if op.Party == "" || op.AuthFrom > op.AuthTo {
		return nfail(InvalidParam), "签署参数非法"
	}
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	if op.Amendment >= len(c.amendments) || op.Amendment < 0 {
		return nfail(NotFound), "协议不存在"
	}
	a := c.amendments[op.Amendment]
	if n.partyIndex(c, op.Party) < 0 {
		return nfail(InvalidParam), "签署方不是合同双方之一"
	}
	if !n.active(c, op.Now) {
		return nfail(StateNotAllowed), "合同不在可变更状态"
	}
	if a.complete {
		return nfail(StateNotAllowed), "协议已签署完成"
	}
	if _, dup := a.signs[op.Party]; dup {
		return nfail(StateNotAllowed), "该方已签署"
	}
	if op.Now < op.AuthFrom || op.Now > op.AuthTo {
		return nfail(AuthExpired), "不在授权有效期内"
	}
	if a.firstSign >= 0 && op.Now > a.firstSign+c.p.SignWindowDays {
		return nfail(SignExpired), "超过补签期限"
	}
	a.signs[op.Party] = op.Now
	if a.firstSign < 0 {
		a.firstSign = op.Now
	}
	if len(a.signs) == 2 {
		a.complete = true
		a.completeDay = op.Now
		a.completeSeq = n.seq
	}
	n.accept(op.Now)
	return OpResult{}, "签署成功"
}

func (n *naive) countersign(op Op) (OpResult, string) {
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	if op.Amendment >= len(c.amendments) || op.Amendment < 0 {
		return nfail(NotFound), "协议不存在"
	}
	a := c.amendments[op.Amendment]
	if !n.active(c, op.Now) {
		return nfail(StateNotAllowed), "合同不在可变更状态"
	}
	if !a.needsCountersign {
		return nfail(StateNotAllowed), "协议不涉及锁定条款"
	}
	if a.countersigned {
		return nfail(StateNotAllowed), "协议已会签"
	}
	a.countersigned = true
	a.countersignDay = op.Now
	n.accept(op.Now)
	return OpResult{}, "会签成功"
}

func (n *naive) notice(op Op) (OpResult, string) {
	if op.Party == "" {
		return nfail(InvalidParam), "通知方为空"
	}
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	if n.partyIndex(c, op.Party) < 0 {
		return nfail(InvalidParam), "通知方不是合同双方之一"
	}
	if !n.active(c, op.Now) {
		return nfail(StateNotAllowed), "合同不在可变更状态"
	}
	c.notices = append(c.notices, naiveNotice{day: op.Now, party: op.Party})
	n.accept(op.Now)
	return OpResult{}, "通知已接受"
}

func (n *naive) terminate(op Op) (OpResult, string) {
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	if c.terminated {
		return nfail(StateNotAllowed), "合同已终止"
	}
	if !c.inForce(op.Now) {
		return nfail(StateNotAllowed), "合同不在期"
	}
	c.terminated = true
	c.termDay = op.Now
	n.accept(op.Now)
	return OpResult{}, "合同已终止"
}

func (n *naive) queryValue(op Op) (OpResult, string) {
	if op.Day < 0 {
		return nfail(InvalidParam), "查询日为负"
	}
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	if _, ok := c.p.Clauses[op.Clause]; !ok {
		return nfail(InvalidParam), "条款不在合同内"
	}
	v, src, why := c.valueAt(op.Clause, op.Day)
	n.accept(op.Now)
	if src < 0 {
		return OpResult{Value: v, Source: Source{Master: true, AmendmentID: -1}}, why
	}
	return OpResult{Value: v, Source: Source{Master: false, AmendmentID: src}},
		"胜出协议见 Source（生效日最晚，并列取签署完成时刻较后者）"
}

func (n *naive) queryInForce(op Op) (OpResult, string) {
	if op.Day < 0 {
		return nfail(InvalidParam), "查询日为负"
	}
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	in := c.inForce(op.Day)
	n.accept(op.Now)
	return OpResult{InForce: in}, "逐任期重推演"
}

func (n *naive) queryExpiry(op Op) (OpResult, string) {
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	exp := c.currentExpiry(op.Now)
	n.accept(op.Now)
	return OpResult{Expiry: exp}, "逐任期重推演"
}

func (n *naive) queryRenewals(op Op) (OpResult, string) {
	if cat, ok := n.checkClock(op.Now); !ok {
		return nfail(cat), "时钟校验失败"
	}
	c := n.getContract(op.Contract)
	if c == nil {
		return nfail(NotFound), "合同不存在"
	}
	renewals := c.renewalsUpTo(op.Now)
	n.accept(op.Now)
	return OpResult{Renewals: renewals}, "逐任期重推演"
}
