package contract

import "sync"

// Service 是合同版本叠加与自动续签服务。
// 全部操作经 Apply 进入，由互斥锁串行化，因此并发调用等价于某个串行顺序；
// 被接受的操作按序记入日志，相同操作序列重放得到完全相同的结果。
type Service struct {
	mu        sync.Mutex
	lastNow   int // 上一次被接受操作的 now
	seq       int64
	contracts map[int]*Contract
	log       []LogEntry
}

// NewService 创建空服务。
func NewService() *Service {
	return &Service{contracts: map[int]*Contract{}}
}

// Apply 执行一个操作并返回结果。被拒绝的操作不改变任何状态与时钟。
func (s *Service) Apply(op Op) OpResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := s.apply(op)
	s.log = append(s.log, LogEntry{Op: op, Result: res})
	return res
}

// Log 返回迄今全部操作（含被拒绝的）及其结果的日志副本。
func (s *Service) Log() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogEntry(nil), s.log...)
}

// Replay 在全新服务上按序重放操作，返回各步结果，用于验证重放确定性。
func Replay(ops []Op) []OpResult {
	s := NewService()
	out := make([]OpResult, 0, len(ops))
	for _, op := range ops {
		out = append(out, s.Apply(op))
	}
	return out
}

func (s *Service) apply(op Op) OpResult {
	switch op.Kind {
	case OpCreateContract:
		return s.createContract(op)
	case OpCreateAmendment:
		return s.createAmendment(op)
	case OpSign:
		return s.sign(op)
	case OpCountersign:
		return s.countersign(op)
	case OpNotice:
		return s.nonRenewalNotice(op)
	case OpTerminate:
		return s.terminate(op)
	case OpQueryValue:
		return s.queryValue(op)
	case OpQueryInForce:
		return s.queryInForce(op)
	case OpQueryExpiry:
		return s.queryExpiry(op)
	case OpQueryRenewals:
		return s.queryRenewals(op)
	}
	return fail(InvalidParam, "unknown op kind %d", int(op.Kind))
}

func fail(cat Category, format string, args ...any) OpResult {
	e := errf(cat, format, args...)
	return OpResult{Err: e.Cat, Message: e.Msg}
}

func failErr(e *Error) OpResult {
	return OpResult{Err: e.Cat, Message: e.Msg}
}

// checkClock 校验时钟：now 须为非负且不小于上一次被接受操作的 now。
func (s *Service) checkClock(now int) *Error {
	if now < 0 {
		return errf(InvalidParam, "now must be >= 0, got %d", now)
	}
	if now < s.lastNow {
		return errf(ClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	return nil
}

// accept 提交操作：推进时钟与全局序号。
func (s *Service) accept(now int) {
	s.lastNow = now
	s.seq++
}

// getContract 查找合同，不存在时返回 NotFound 结果。
func (s *Service) getContract(id int) (*Contract, *OpResult) {
	c := s.contracts[id]
	if c == nil {
		r := fail(NotFound, "contract %d does not exist", id)
		return nil, &r
	}
	return c, nil
}

// ensureActive 校验合同在 now 处于可变更状态（在期且未终止）。
func (c *Contract) ensureActive(now, committed int) *Error {
	if c.terminated {
		return errf(StateNotAllowed, "contract %d terminated at day %d", c.id, c.termDay)
	}
	if !c.inForceAt(now, committed) {
		return errf(StateNotAllowed, "contract %d not in force at day %d", c.id, now)
	}
	return nil
}

func validateContractParams(p ContractParams) *Error {
	if p.Parties[0] == "" || p.Parties[1] == "" {
		return errf(InvalidParam, "parties must be non-empty")
	}
	if p.Parties[0] == p.Parties[1] {
		return errf(InvalidParam, "parties must be distinct")
	}
	if len(p.Clauses) == 0 {
		return errf(InvalidParam, "contract must contain at least one clause")
	}
	if p.Start < 0 {
		return errf(InvalidParam, "start day must be >= 0")
	}
	if p.Expiry < p.Start {
		return errf(InvalidParam, "expiry %d < start %d", p.Expiry, p.Start)
	}
	if p.SignWindowDays < 0 {
		return errf(InvalidParam, "sign window must be >= 0")
	}
	for _, k := range p.Locked {
		if _, ok := p.Clauses[k]; !ok {
			return errf(InvalidParam, "locked clause %d not in clause set", k)
		}
	}
	for _, k := range []int{p.AutoRenewClause, p.PeriodClause, p.NoticeClause} {
		if _, ok := p.Clauses[k]; !ok {
			return errf(InvalidParam, "renewal clause %d not in clause set", k)
		}
	}
	return nil
}

func (s *Service) createContract(op Op) OpResult {
	if e := validateContractParams(op.Params); e != nil {
		return failErr(e)
	}
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	id := len(s.contracts)
	s.contracts[id] = newContract(id, op.Params)
	s.accept(op.Now)
	return OpResult{ContractID: id}
}

func (s *Service) createAmendment(op Op) OpResult {
	// 1. 参数非法（语法层面）
	if op.DeclaredEffDay < 0 {
		return fail(InvalidParam, "declared effective day must be >= 0, got %d", op.DeclaredEffDay)
	}
	if op.IsRevocation {
		if len(op.Mods) != 0 {
			return fail(InvalidParam, "revocation amendment cannot carry modifications")
		}
		if op.RevokeTarget < 0 {
			return fail(InvalidParam, "revocation target must be >= 0")
		}
	} else if len(op.Mods) == 0 {
		return fail(InvalidParam, "modification amendment must modify at least one clause")
	}
	// 2. 时钟回退
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	// 3. 合同或协议不存在
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	var target *Amendment
	if op.IsRevocation {
		target = c.amendments[op.RevokeTarget]
		if target == nil {
			return fail(NotFound, "amendment %d does not exist in contract %d", op.RevokeTarget, c.id)
		}
	}
	// 语义层面参数校验（依赖合同）
	if !op.IsRevocation {
		for clause := range op.Mods {
			if _, ok := c.master[clause]; !ok {
				return fail(InvalidParam, "clause %d not in contract %d", clause, c.id)
			}
		}
	}
	// 4. 状态不允许
	if e := c.ensureActive(op.Now, s.lastNow); e != nil {
		return failErr(e)
	}
	if op.IsRevocation {
		if !target.complete {
			return fail(StateNotAllowed, "target amendment %d not fully signed yet", target.id)
		}
		// 5. 缺少会签：目标已签完但缺法务会签，尚未生效，不可撤销
		if target.needsCountersign && !target.countersigned {
			return fail(CountersignMissing, "target amendment %d touches locked clauses but lacks legal countersign", target.id)
		}
		if !target.effective || target.effDay > op.Now {
			return fail(StateNotAllowed, "target amendment %d not yet effective", target.id)
		}
	}
	a := c.addAmendment(op.DeclaredEffDay, op.Mods, boolToTarget(op.IsRevocation, op.RevokeTarget))
	s.accept(op.Now)
	return OpResult{AmendmentID: a.id}
}

func boolToTarget(isRev bool, target int) int {
	if isRev {
		return target
	}
	return -1
}

func (s *Service) sign(op Op) OpResult {
	// 1. 参数非法
	if op.Party == "" {
		return fail(InvalidParam, "party must be non-empty")
	}
	if op.AuthFrom > op.AuthTo {
		return fail(InvalidParam, "auth window [%d, %d] is inverted", op.AuthFrom, op.AuthTo)
	}
	// 2. 时钟回退
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	// 3. 合同或协议不存在
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	a := c.amendments[op.Amendment]
	if a == nil {
		return fail(NotFound, "amendment %d does not exist in contract %d", op.Amendment, c.id)
	}
	// 语义层面参数校验（依赖合同）
	if c.partyIndex(op.Party) < 0 {
		return fail(InvalidParam, "party %q is not a party of contract %d", op.Party, c.id)
	}
	// 4. 状态不允许
	if e := c.ensureActive(op.Now, s.lastNow); e != nil {
		return failErr(e)
	}
	if e := c.signable(a, op.Party, op.Now); e != nil {
		return failErr(e)
	}
	// 5. 授权失效
	if op.Now < op.AuthFrom || op.Now > op.AuthTo {
		return fail(AuthExpired, "signing at day %d outside auth window [%d, %d]", op.Now, op.AuthFrom, op.AuthTo)
	}
	// 6. 签署超期（恰等于窗口天数仍可签）
	if a.firstSign >= 0 && op.Now > a.firstSign+c.signWin {
		return fail(SignExpired, "second signature at day %d exceeds deadline %d", op.Now, a.firstSign+c.signWin)
	}
	c.applySign(a, op.Party, op.Now, s.seq)
	s.accept(op.Now)
	return OpResult{}
}

func (s *Service) countersign(op Op) OpResult {
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	a := c.amendments[op.Amendment]
	if a == nil {
		return fail(NotFound, "amendment %d does not exist in contract %d", op.Amendment, c.id)
	}
	if e := c.ensureActive(op.Now, s.lastNow); e != nil {
		return failErr(e)
	}
	if !a.needsCountersign {
		return fail(StateNotAllowed, "amendment %d does not touch locked clauses", a.id)
	}
	if a.countersigned {
		return fail(StateNotAllowed, "amendment %d already countersigned", a.id)
	}
	c.applyCountersign(a, op.Now)
	s.accept(op.Now)
	return OpResult{}
}

func (s *Service) nonRenewalNotice(op Op) OpResult {
	if op.Party == "" {
		return fail(InvalidParam, "party must be non-empty")
	}
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	if c.partyIndex(op.Party) < 0 {
		return fail(InvalidParam, "party %q is not a party of contract %d", op.Party, c.id)
	}
	if e := c.ensureActive(op.Now, s.lastNow); e != nil {
		return failErr(e)
	}
	c.notices = append(c.notices, notice{day: op.Now, party: op.Party})
	s.accept(op.Now)
	return OpResult{}
}

func (s *Service) terminate(op Op) OpResult {
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	if c.terminated {
		return fail(StateNotAllowed, "contract %d already terminated at day %d", c.id, c.termDay)
	}
	if !c.inForceAt(op.Now, s.lastNow) {
		return fail(StateNotAllowed, "contract %d not in force at day %d", c.id, op.Now)
	}
	c.applyTerminate(op.Now)
	s.accept(op.Now)
	return OpResult{}
}

func (s *Service) queryValue(op Op) OpResult {
	if op.Day < 0 {
		return fail(InvalidParam, "query day must be >= 0, got %d", op.Day)
	}
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	if _, ok := c.master[op.Clause]; !ok {
		return fail(InvalidParam, "clause %d not in contract %d", op.Clause, c.id)
	}
	v, src := c.valueAt(op.Clause, op.Day)
	s.accept(op.Now)
	return OpResult{Value: v, Source: Source{Master: src < 0, AmendmentID: src}}
}

func (s *Service) queryInForce(op Op) OpResult {
	if op.Day < 0 {
		return fail(InvalidParam, "query day must be >= 0, got %d", op.Day)
	}
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	in := c.inForceAt(op.Day, s.lastNow)
	s.accept(op.Now)
	return OpResult{InForce: in}
}

func (s *Service) queryExpiry(op Op) OpResult {
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	exp := c.currentExpiry(op.Now)
	s.accept(op.Now)
	return OpResult{Expiry: exp}
}

func (s *Service) queryRenewals(op Op) OpResult {
	if e := s.checkClock(op.Now); e != nil {
		return failErr(e)
	}
	c, r := s.getContract(op.Contract)
	if r != nil {
		return *r
	}
	renewals := c.renewalHistory(op.Now)
	s.accept(op.Now)
	return OpResult{Renewals: renewals}
}
