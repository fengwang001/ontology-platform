package ffm

// NaiveSystem 是按本题规则独立写成的朴素对照模型。
// 它不引用 System/account 的增量记账实现，只保留不可变配置与“每个账户一条追加式事件日志”；
// 每次判定都从开通事件起顺序重放，用最直白的 map 重建状态。

type naiveEvent struct {
	kind     string
	now      int64
	seg      Segment
	id       string
	miles    int64
	openTime int64
}

// NaiveSystem 朴素对照模型。
type NaiveSystem struct {
	cfg    Config
	opened map[string]bool
	logs   map[string][]naiveEvent
}

// NewNaive 创建朴素模型。
func NewNaive(c Config) (*NaiveSystem, error) {
	if err := validateConfig(c); err != nil {
		return nil, err
	}
	return &NaiveSystem{cfg: c, opened: make(map[string]bool), logs: make(map[string][]naiveEvent)}, nil
}

type naiveState struct {
	openTime int64
	lastOp   int64
	activeAt int64
	tier     int
	period   int64
	periodQM map[int64]int64 // 每个周期各自的定级累计，刻意不压缩、不丢弃
	balance  int64
	debt     int64

	segState  map[string]string // "", "posted", "voided"
	segBase   map[string]int64
	segBonus  map[string]int64
	segPeriod map[string]int64
	recMiles  map[string]int64
	recCancel map[string]bool
}

func newNaiveState(openTime int64) *naiveState {
	return &naiveState{
		openTime:  openTime,
		lastOp:    openTime,
		activeAt:  openTime,
		periodQM:  map[int64]int64{0: 0},
		segState:  map[string]string{},
		segBase:   map[string]int64{},
		segBonus:  map[string]int64{},
		segPeriod: map[string]int64{},
		recMiles:  map[string]int64{},
		recCancel: map[string]bool{},
	}
}

func (n *NaiveSystem) periodOf(openTime, t int64) int64 {
	return (t - openTime) / n.cfg.PeriodLength
}

func (n *NaiveSystem) settle(st *naiveState, idxNow int64) {
	for st.period < idxNow {
		qm := st.periodQM[st.period]
		st.tier = settleAfterLevel(st.tier, qm, n.cfg)
		st.period++
		if _, ok := st.periodQM[st.period]; !ok {
			st.periodQM[st.period] = 0
		}
	}
}

func (n *NaiveSystem) snapshot(st *naiveState, now int64) Snapshot {
	idxNow := n.periodOf(st.openTime, now)
	tier := st.tier
	for p := st.period; p < idxNow; p++ {
		tier = settleAfterLevel(tier, st.periodQM[p], n.cfg)
	}
	qm := st.periodQM[st.period]
	if idxNow > st.period {
		qm = 0
	}
	return Snapshot{
		Tier: tier, Qualifying: qm, Redeemable: st.balance, Debt: st.debt,
		LastOpTime: st.lastOp, ActiveTime: st.activeAt,
	}
}

// replay 只应用已接受事件，重建当前状态。
func (n *NaiveSystem) replay(acctID string) *naiveState {
	var st *naiveState
	for _, e := range n.logs[acctID] {
		switch e.kind {
		case "open":
			st = newNaiveState(e.openTime)
		case "post":
			n.settle(st, n.periodOf(st.openTime, e.now))
			base := baseMiles(e.seg, n.cfg)
			bonus := base * n.cfg.Bonuses[st.tier] / 100
			amount := base + bonus
			if st.debt > 0 {
				if amount >= st.debt {
					st.balance += amount - st.debt
					st.debt = 0
				} else {
					st.debt -= amount
				}
			} else {
				st.balance += amount
			}
			pidx := n.periodOf(st.openTime, e.seg.FlightTime)
			if pidx == st.period {
				st.periodQM[pidx] += base
				st.tier = tierForQM(st.periodQM[pidx], n.cfg)
			}
			st.segState[e.seg.ID] = "posted"
			st.segBase[e.seg.ID] = base
			st.segBonus[e.seg.ID] = bonus
			st.segPeriod[e.seg.ID] = pidx
			st.lastOp, st.activeAt = e.now, e.now
		case "refund_void":
			st.segState[e.id] = "voided"
			st.lastOp = e.now
		case "refund_posted":
			n.settle(st, n.periodOf(st.openTime, e.now))
			pid := st.segPeriod[e.id]
			if pid == st.period {
				st.periodQM[pid] -= st.segBase[e.id]
			}
			amount := st.segBase[e.id] + st.segBonus[e.id]
			if amount <= st.balance {
				st.balance -= amount
			} else {
				st.debt += amount - st.balance
				st.balance = 0
			}
			st.segState[e.id] = "voided"
			st.lastOp = e.now
		case "redeem":
			n.settle(st, n.periodOf(st.openTime, e.now))
			st.balance -= e.miles
			st.recMiles[e.id] = e.miles
			st.lastOp, st.activeAt = e.now, e.now
		case "cancel":
			n.settle(st, n.periodOf(st.openTime, e.now))
			gross := st.recMiles[e.id] - n.cfg.CancelFee
			if gross < 0 {
				gross = 0
			}
			if st.debt > 0 {
				if gross >= st.debt {
					st.balance += gross - st.debt
					st.debt = 0
				} else {
					st.debt -= gross
				}
			} else {
				st.balance += gross
			}
			st.recCancel[e.id] = true
			st.lastOp = e.now
		case "unfreeze":
			n.settle(st, n.periodOf(st.openTime, e.now))
			st.activeAt = e.now
			st.lastOp = e.now
		}
	}
	return st
}

func (n *NaiveSystem) frozen(st *naiveState, now int64) bool {
	return now-st.activeAt >= n.cfg.InactiveDuration
}

// OpenAccount 朴素模型开通。
func (n *NaiveSystem) OpenAccount(id string, now int64) error {
	if id == "" || now < 0 {
		return errWith(ErrInvalidParam, "参数非法")
	}
	if n.opened[id] {
		return errWith(ErrInvalidParam, "参数非法：账户已存在")
	}
	n.opened[id] = true
	n.logs[id] = []naiveEvent{{kind: "open", now: now, openTime: now}}
	return nil
}

// Post 朴素模型入账：判定时完整重放，成功后只追加事件。
func (n *NaiveSystem) Post(acctID string, now int64, seg Segment) (*PostResult, error) {
	if acctID == "" || seg.ID == "" || seg.Distance <= 0 || seg.Rate < 0 || seg.Rate > 300 ||
		seg.FlightTime < 0 || now < 0 {
		return nil, errWith(ErrInvalidParam, "参数非法")
	}
	if !n.opened[acctID] {
		return nil, errWith(ErrAccountNotExist, "账户不存在")
	}
	st := n.replay(acctID)
	if now < st.lastOp {
		return nil, errWith(ErrClockBack, "时钟回退")
	}
	if n.frozen(st, now) {
		return nil, errWith(ErrAccountFrozen, "账户冻结")
	}
	if state := st.segState[seg.ID]; state == "posted" || state == "voided" {
		return nil, errWith(ErrDuplicatePosting, "重复入账")
	}
	if seg.FlightTime > now {
		return nil, errWith(ErrInvalidParam, "参数非法：飞行时刻晚于入账时刻")
	}
	if now > seg.FlightTime+n.cfg.RetroWindow {
		return nil, errWith(ErrLatePosting, "补登超期")
	}
	if seg.FlightTime < st.openTime {
		return nil, errWith(ErrInvalidParam, "参数非法：飞行时刻早于开通时刻")
	}
	n.settle(st, n.periodOf(st.openTime, now))
	tierBefore := st.tier
	base := baseMiles(seg, n.cfg)
	bonus := base * n.cfg.Bonuses[tierBefore] / 100
	amount := base + bonus
	var toBalance, toDebt int64
	if st.debt > 0 {
		if amount >= st.debt {
			toDebt, toBalance = st.debt, amount-st.debt
			st.debt = 0
			st.balance += toBalance
		} else {
			toDebt = amount
			st.debt -= amount
		}
	} else {
		toBalance = amount
		st.balance += amount
	}
	pidx := n.periodOf(st.openTime, seg.FlightTime)
	if pidx == st.period {
		st.periodQM[pidx] += base
		st.tier = tierForQM(st.periodQM[pidx], n.cfg)
	}
	n.logs[acctID] = append(n.logs[acctID], naiveEvent{kind: "post", now: now, seg: seg})
	return &PostResult{
		Base: base, Bonus: bonus, Redeemable: toBalance, ToDebt: toDebt,
		TierBefore: tierBefore, TierAfter: st.tier, PeriodIndex: pidx,
	}, nil
}

// Refund 朴素模型退票。
func (n *NaiveSystem) Refund(acctID, segmentID string, now int64) error {
	if acctID == "" || segmentID == "" || now < 0 {
		return errWith(ErrInvalidParam, "参数非法")
	}
	if !n.opened[acctID] {
		return errWith(ErrAccountNotExist, "账户不存在")
	}
	st := n.replay(acctID)
	if now < st.lastOp {
		return errWith(ErrClockBack, "时钟回退")
	}
	kind := "refund_void"
	if st.segState[segmentID] == "posted" {
		kind = "refund_posted"
	}
	n.logs[acctID] = append(n.logs[acctID], naiveEvent{kind: kind, now: now, id: segmentID})
	return nil
}

// Redeem 朴素模型兑换。
func (n *NaiveSystem) Redeem(acctID, recordID string, now, miles int64) (*RedeemResult, error) {
	if acctID == "" || recordID == "" || now < 0 || miles <= 0 {
		return nil, errWith(ErrInvalidParam, "参数非法")
	}
	if !n.opened[acctID] {
		return nil, errWith(ErrAccountNotExist, "账户不存在")
	}
	st := n.replay(acctID)
	if now < st.lastOp {
		return nil, errWith(ErrClockBack, "时钟回退")
	}
	if n.frozen(st, now) {
		return nil, errWith(ErrAccountFrozen, "账户冻结")
	}
	if _, ok := st.recMiles[recordID]; ok {
		return nil, errWith(ErrInvalidParam, "参数非法：兑换记录 ID 重复")
	}
	if st.debt > 0 || st.balance < miles {
		return nil, errWith(ErrInsufficientMiles, "里程不足")
	}
	n.logs[acctID] = append(n.logs[acctID], naiveEvent{kind: "redeem", now: now, id: recordID, miles: miles})
	return &RedeemResult{RecordID: recordID, Spent: miles, Balance: st.balance - miles}, nil
}

// CancelRedeem 朴素模型取消兑换。
func (n *NaiveSystem) CancelRedeem(acctID, recordID string, now int64) (*CancelResult, error) {
	if acctID == "" || recordID == "" || now < 0 {
		return nil, errWith(ErrInvalidParam, "参数非法")
	}
	if !n.opened[acctID] {
		return nil, errWith(ErrAccountNotExist, "账户不存在")
	}
	st := n.replay(acctID)
	if now < st.lastOp {
		return nil, errWith(ErrClockBack, "时钟回退")
	}
	if n.frozen(st, now) {
		return nil, errWith(ErrAccountFrozen, "账户冻结")
	}
	miles, ok := st.recMiles[recordID]
	if !ok {
		return nil, errWith(ErrRedemptionNotExist, "兑换记录不存在")
	}
	if st.recCancel[recordID] {
		return nil, errWith(ErrRedemptionCancelled, "兑换记录已取消")
	}
	n.settle(st, n.periodOf(st.openTime, now))
	gross := miles - n.cfg.CancelFee
	if gross < 0 {
		gross = 0
	}
	var toBalance, toDebt int64
	if st.debt > 0 {
		if gross >= st.debt {
			toDebt, toBalance = st.debt, gross-st.debt
			st.debt = 0
			st.balance += toBalance
		} else {
			toDebt = gross
			st.debt -= gross
		}
	} else {
		toBalance = gross
		st.balance += gross
	}
	n.logs[acctID] = append(n.logs[acctID], naiveEvent{kind: "cancel", now: now, id: recordID})
	return &CancelResult{Refunded: toBalance, ToDebt: toDebt, Balance: st.balance, Debt: st.debt}, nil
}

// Unfreeze 朴素模型解冻。
func (n *NaiveSystem) Unfreeze(acctID string, now int64) error {
	if acctID == "" || now < 0 {
		return errWith(ErrInvalidParam, "参数非法")
	}
	if !n.opened[acctID] {
		return errWith(ErrAccountNotExist, "账户不存在")
	}
	st := n.replay(acctID)
	if now < st.lastOp {
		return errWith(ErrClockBack, "时钟回退")
	}
	n.logs[acctID] = append(n.logs[acctID], naiveEvent{kind: "unfreeze", now: now})
	return nil
}

// Query 朴素模型查询：完整重放后按当前时刻投影。
func (n *NaiveSystem) Query(acctID string, now int64) (Snapshot, error) {
	if acctID == "" || now < 0 {
		return Snapshot{}, errWith(ErrInvalidParam, "参数非法")
	}
	if !n.opened[acctID] {
		return Snapshot{}, errWith(ErrAccountNotExist, "账户不存在")
	}
	st := n.replay(acctID)
	if now < st.lastOp {
		return Snapshot{}, errWith(ErrClockBack, "时钟回退")
	}
	return n.snapshot(st, now), nil
}
