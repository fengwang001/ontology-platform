package ffm

import "sync"

// System 是线程安全的常旅客里程账户系统。
// 并发语义：账户映射由 accountsMu 保护；账户内状态由每个账户自己的 mu 保护，
// 不同账户可真正并行，同一账户的操作等价于按某种全序串行执行。
type System struct {
	cfg        Config
	mu         sync.RWMutex
	accounts   map[string]*account
	openNextID int64
}

// NewSystem 校验配置并创建系统。
func NewSystem(c Config) (*System, error) {
	if err := validateConfig(c); err != nil {
		return nil, err
	}
	return &System{cfg: c, accounts: make(map[string]*account)}, nil
}

func validateConfig(c Config) error {
	if c.RetroWindow < 0 || c.MinMiles < 0 || c.InactiveDuration < 0 || c.CancelFee < 0 || c.PeriodLength <= 0 {
		return errWith(ErrInvalidParam, "配置项非法：窗口/保底/不活跃时长/手续费需非负，周期长度需为正")
	}
	if c.Thresholds[0] < 0 || c.Thresholds[0] >= c.Thresholds[1] || c.Thresholds[1] >= c.Thresholds[2] {
		return errWith(ErrInvalidParam, "配置项非法：三个升级门槛必须严格递增且非负")
	}
	for _, b := range c.Bonuses {
		if b < 0 {
			return errWith(ErrInvalidParam, "配置项非法：等级加成不能为负")
		}
	}
	return nil
}

func validateSegment(seg Segment) bool {
	return seg.ID != "" && seg.Distance > 0 && seg.Rate >= 0 && seg.Rate <= 300 && seg.FlightTime >= 0
}

func validID(id string) bool { return id != "" }

// baseMiles 计算基础里程：ceil(distance*rate/100)，不足保底按保底。
func baseMiles(seg Segment, c Config) int64 {
	v := (seg.Distance*seg.Rate + 99) / 100
	if v < c.MinMiles {
		v = c.MinMiles
	}
	return v
}

// getAccount 在 RLock 下取出账户指针（取指针后释放 map 锁，再用账户锁）。
func (s *System) getAccount(id string) *account {
	s.mu.RLock()
	a := s.accounts[id]
	s.mu.RUnlock()
	return a
}

// frozenLocked 判定冻结：now-活跃锚点 >= 不活跃时长（恰等即冻结）。
func (a *account) frozenLocked(now int64) bool {
	return now-a.activeAt >= a.cfg.InactiveDuration
}

// OpenAccount 开通账户，返回账户 ID。
func (s *System) OpenAccount(id string, now int64) error {
	// 拒绝次序：参数非法 > 时钟回退（开通时没有更早时钟，仅校验非负）> 账户不存在。
	if !validID(id) || now < 0 {
		return errWith(ErrInvalidParam, "参数非法：账户 ID 不能为空，时刻不能为负")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; ok {
		return errWith(ErrInvalidParam, "参数非法：账户已存在")
	}
	a := newAccount(id, now, &s.cfg)
	s.accounts[id] = a
	return nil
}

// Post 航段飞行后入账。
func (s *System) Post(acctID string, now int64, seg Segment) (*PostResult, error) {
	if !validID(acctID) || !validateSegment(seg) || now < 0 {
		return nil, errWith(ErrInvalidParam, "参数非法")
	}
	a := s.getAccount(acctID)
	if a == nil {
		return nil, errWith(ErrAccountNotExist, "账户不存在")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.lastOp {
		return nil, errWith(ErrClockBack, "时钟回退")
	}
	// 冻结判定只看时刻与活跃锚点，不提前结算周期。
	if a.frozenLocked(now) {
		return nil, errWith(ErrAccountFrozen, "账户冻结")
	}
	if st, ok := a.segments[seg.ID]; ok && (st.posted || st.voided) {
		return nil, errWith(ErrDuplicatePosting, "重复入账")
	}
	if seg.FlightTime > now {
		return nil, errWith(ErrInvalidParam, "参数非法：飞行时刻晚于入账时刻")
	}
	if now > seg.FlightTime+a.cfg.RetroWindow {
		return nil, errWith(ErrLatePosting, "补登超期")
	}
	if seg.FlightTime < a.openTime {
		return nil, errWith(ErrInvalidParam, "参数非法：飞行时刻早于账户开通时刻")
	}

	idxNow := a.periodIndexOf(now)
	a.settleTo(idxNow)

	tierBefore := a.tier
	base := baseMiles(seg, *a.cfg)
	bonus := base * a.cfg.Bonuses[tierBefore] / 100
	redeem := base + bonus
	toBalance, toDebt := a.creditRedeemable(redeem)

	idxFlight := a.periodIndexOf(seg.FlightTime)
	if idxFlight == a.periodIdx {
		// 记入当前周期：触发同次入账后的立即升级（本次已按升级前等级加成）。
		a.periodQM += base
		a.tier = tierForQM(a.periodQM, *a.cfg)
	}
	// 若航段属于已结束周期：定级里程仅体现在历史航段记录上，
	// 等级与当前周期累计均不变（朴素模型重放时同样忽略其对等级的影响）。

	a.segments[seg.ID] = &segmentState{
		seg:       seg,
		posted:    true,
		base:      base,
		bonus:     bonus,
		postTime:  now,
		periodIdx: idxFlight,
	}
	a.lastOp = now
	a.activeAt = now
	return &PostResult{
		Base:        base,
		Bonus:       bonus,
		Redeemable:  toBalance,
		ToDebt:      toDebt,
		TierBefore:  tierBefore,
		TierAfter:   a.tier,
		PeriodIndex: idxFlight,
	}, nil
}

// Refund 航段退票，撤销其入账（幂等：未入账退票静默成功但封锁该航段）。
func (s *System) Refund(acctID, segmentID string, now int64) error {
	if !validID(acctID) || !validID(segmentID) || now < 0 {
		return errWith(ErrInvalidParam, "参数非法")
	}
	a := s.getAccount(acctID)
	if a == nil {
		return errWith(ErrAccountNotExist, "账户不存在")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.lastOp {
		return errWith(ErrClockBack, "时钟回退")
	}
	// 冻结期间扣回照常执行。
	idxNow := a.periodIndexOf(now)
	a.settleTo(idxNow)

	st, ok := a.segments[segmentID]
	if !ok {
		// 未入账航段退票：不报错、不改余额/等级，但该航段此后不可再入账。
		a.segments[segmentID] = &segmentState{voided: true}
		a.lastOp = now
		return nil
	}
	if st.posted && !st.voided {
		// 按当初实际入账数额扣回，不按当前等级重算。
		if st.periodIdx == a.periodIdx {
			a.periodQM -= st.base
		}
		a.debitRedeemable(st.base + st.bonus)
		st.voided = true
	}
	// 已退票再退票：幂等无变化，但仍是被接受操作，推进时钟。
	a.lastOp = now
	return nil
}

// Redeem 兑换奖励票。
func (s *System) Redeem(acctID, recordID string, now, miles int64) (*RedeemResult, error) {
	if !validID(acctID) || !validID(recordID) || now < 0 || miles <= 0 {
		return nil, errWith(ErrInvalidParam, "参数非法")
	}
	a := s.getAccount(acctID)
	if a == nil {
		return nil, errWith(ErrAccountNotExist, "账户不存在")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.lastOp {
		return nil, errWith(ErrClockBack, "时钟回退")
	}
	if a.frozenLocked(now) {
		return nil, errWith(ErrAccountFrozen, "账户冻结")
	}
	if _, ok := a.records[recordID]; ok {
		return nil, errWith(ErrInvalidParam, "参数非法：兑换记录 ID 重复")
	}
	// 有欠账时可兑换能力视为 0。
	if a.debt > 0 || a.balance < miles {
		return nil, errWith(ErrInsufficientMiles, "里程不足")
	}
	a.settleTo(a.periodIndexOf(now))
	a.balance -= miles
	a.records[recordID] = &redemptionState{id: recordID, miles: miles, createdAt: now}
	a.lastOp = now
	a.activeAt = now
	return &RedeemResult{RecordID: recordID, Spent: miles, Balance: a.balance}, nil
}

// CancelRedeem 取消兑换。
func (s *System) CancelRedeem(acctID, recordID string, now int64) (*CancelResult, error) {
	if !validID(acctID) || !validID(recordID) || now < 0 {
		return nil, errWith(ErrInvalidParam, "参数非法")
	}
	a := s.getAccount(acctID)
	if a == nil {
		return nil, errWith(ErrAccountNotExist, "账户不存在")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.lastOp {
		return nil, errWith(ErrClockBack, "时钟回退")
	}
	if a.frozenLocked(now) {
		return nil, errWith(ErrAccountFrozen, "账户冻结")
	}
	st, ok := a.records[recordID]
	if !ok {
		return nil, errWith(ErrRedemptionNotExist, "兑换记录不存在")
	}
	if st.cancelled {
		return nil, errWith(ErrRedemptionCancelled, "兑换记录已取消")
	}
	a.settleTo(a.periodIndexOf(now))
	refundGross := st.miles - a.cfg.CancelFee
	if refundGross < 0 {
		refundGross = 0
	}
	toBalance, toDebt := a.creditRedeemable(refundGross)
	st.cancelled = true
	st.cancelledAt = now
	a.lastOp = now
	return &CancelResult{Refunded: toBalance, ToDebt: toDebt, Balance: a.balance, Debt: a.debt}, nil
}

// Unfreeze 显式解冻，活跃计时从 now 重新起算。
func (s *System) Unfreeze(acctID string, now int64) error {
	if !validID(acctID) || now < 0 {
		return errWith(ErrInvalidParam, "参数非法")
	}
	a := s.getAccount(acctID)
	if a == nil {
		return errWith(ErrAccountNotExist, "账户不存在")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.lastOp {
		return errWith(ErrClockBack, "时钟回退")
	}
	// 解冻不是“入账或兑换”，不重置 activeAt 为已发生活动；而是显式重新起算锚点。
	// 周期结算也在此推进，保证解冻后 Query 等级结论一致。
	a.settleTo(a.periodIndexOf(now))
	a.activeAt = now
	a.lastOp = now
	return nil
}

// Query 返回当前等级、两类里程、欠账与计时锚点（O(1)）。
func (s *System) Query(acctID string, now int64) (Snapshot, error) {
	if !validID(acctID) || now < 0 {
		return Snapshot{}, errWith(ErrInvalidParam, "参数非法")
	}
	a := s.getAccount(acctID)
	if a == nil {
		return Snapshot{}, errWith(ErrAccountNotExist, "账户不存在")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if now < a.lastOp {
		return Snapshot{}, errWith(ErrClockBack, "时钟回退")
	}
	// 查询不改状态、不推进时钟；等级结论以纯函数投影“当前时刻本应发生的结算”。
	idxNow := a.periodIndexOf(now)
	tier := a.projectedTier(idxNow)
	qualifying := a.periodQM
	if idxNow > a.periodIdx {
		qualifying = 0 // 查询的是新周期的累计；旧周期累计已结算，不作为对外结论
	}
	return Snapshot{
		Tier:       tier,
		Qualifying: qualifying,
		Redeemable: a.balance,
		Debt:       a.debt,
		LastOpTime: a.lastOp,
		ActiveTime: a.activeAt,
	}, nil
}
