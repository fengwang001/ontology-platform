package meter

// Naive 是与 Controller 完全独立的朴素参考实现：
// 每次推进逐 tick 扫描所有挂起效果，电价用线性查找。
// 仅用于随机对照，不考虑性能；与 Controller 的任何
// 状态或事件分歧都说明其中一方判定有误。
type Naive struct {
	cfg    Config
	now    Tick
	status Status

	balance       Money
	debt          Money
	emergencyUsed Money

	cutAt         Tick
	restoreAt     Tick
	lastCum       int64
	hasReading    bool
	lastReadingAt Tick
	warned        bool

	cycle    int64
	emgCycle bool

	cycDeduct Money
	cycWarns  int
	cycCut    int64
	cutSince  Tick

	sumRecharge Money
	sumGrant    Money
	sumDeduct   Money
	sumRepaid   Money

	events []Event
}

func NewNaive(cfg Config, initialBalance Money) *Naive {
	if err := cfg.validate(initialBalance); err != nil {
		return nil
	}
	n := &Naive{cfg: cfg, now: cfg.CreatedAt, balance: initialBalance,
		status: Powered, warned: initialBalance < cfg.WarnThreshold,
		sumRecharge: initialBalance}
	n.emit(Event{Kind: EvCreated, At: cfg.CreatedAt, Balance: initialBalance})
	return n
}

func (n *Naive) emit(e Event) { n.events = append(n.events, e) }

func (n *Naive) priceAt(t Tick) int64 {
	p := n.cfg.Tariffs[0].Price
	for _, tf := range n.cfg.Tariffs {
		if tf.Start <= t {
			p = tf.Price
		}
	}
	return p
}

func (n *Naive) boundaryAt(t Tick) bool {
	return (t-n.cfg.CreatedAt) > 0 &&
		(t-n.cfg.CreatedAt)%n.cfg.CycleLength == 0
}

func (n *Naive) closeCycle(t Tick) {
	if n.status == Cut {
		n.cycCut += t - n.cutSince
		n.cutSince = t
	}
	st, en := n.cfg.cycleBounds(n.cycle)
	n.emit(Event{
		Kind: EvCycleSummary, At: t, Cycle: n.cycle,
		CycleStart: st, CycleEnd: en, Deducted: n.cycDeduct,
		Warns: n.cycWarns, CutDuration: n.cycCut,
	})
	n.cycle++
	n.cycDeduct, n.cycWarns, n.cycCut = 0, 0, 0
	n.emgCycle = false
}

func (n *Naive) doCut(t Tick) {
	if n.status == Cut {
		n.cycCut += t - n.cutSince
	}
	n.debt += -n.balance
	n.balance = 0
	n.status = Cut
	n.cutAt = 0
	n.cutSince = t
	n.emit(Event{Kind: EvCut, At: t, Balance: 0, Debt: n.debt})
}

func (n *Naive) timeoutRestore(t Tick) {
	n.status = Cut
	n.cutSince = t
	n.restoreAt = 0
	n.emit(Event{Kind: EvRestoreTimeout, At: t, Balance: n.balance})
}

func (n *Naive) advance(t Tick) []Event {
	from := len(n.events)
	for n.now < t {
		n.now++
		if n.boundaryAt(n.now) {
			n.closeCycle(n.now)
		}
		if n.status == PendingCut && n.cutAt == n.now {
			n.doCut(n.now)
		}
		if n.status == PendingRestore && n.restoreAt == n.now {
			n.timeoutRestore(n.now)
		}
	}
	return n.events[from:]
}

func (n *Naive) checkTime(t Tick) error {
	if t < n.cfg.CreatedAt {
		return ErrInvalidParam
	}
	if t < n.now {
		return ErrClockBack
	}
	return nil
}

func (n *Naive) updateWarn(t Tick) {
	if n.balance < n.cfg.WarnThreshold {
		if !n.warned {
			n.warned = true
			n.cycWarns++
			n.emit(Event{Kind: EvWarn, At: t, Balance: n.balance})
		}
	} else {
		n.warned = false
	}
}

func (n *Naive) scheduleCut(at Tick) {
	n.cutAt = at
	if n.cfg.inFriendly(at) {
		n.cutAt = n.cfg.friendlyEnd(at)
	}
	if n.cutAt <= at {
		n.doCut(at)
	}
}

func (n *Naive) charge(t Tick, cumulative int64) {
	if !n.hasReading {
		n.hasReading, n.lastCum, n.lastReadingAt = true, cumulative, t
		return
	}
	energy := cumulative - n.lastCum
	price := n.priceAt(n.lastReadingAt)
	n.lastCum, n.lastReadingAt = cumulative, t
	if energy <= 0 {
		return
	}
	amount := floorDiv(energy*price, 1000)
	wasCut := n.status == Cut
	if wasCut {
		n.debt += amount
		n.sumDeduct += amount
		n.cycDeduct += amount
		n.emit(Event{Kind: EvDuringCutUsage, At: t,
			CumulativeBefore: cumulative - energy, Energy: energy,
			Price: price, Amount: amount, Balance: n.balance, Debt: n.debt})
		return
	}
	n.balance -= amount
	n.sumDeduct += amount
	n.cycDeduct += amount
	n.emit(Event{Kind: EvDeduct, At: t,
		CumulativeBefore: cumulative - energy, Energy: energy,
		Price: price, Amount: amount, Balance: n.balance})
	if n.balance < n.cfg.WarnThreshold {
		if !n.warned {
			n.warned = true
			n.cycWarns++
			n.emit(Event{Kind: EvWarn, At: t, Balance: n.balance})
		}
	} else {
		n.warned = false
	}
	if n.balance < 0 {
		switch n.status {
		case Powered:
			n.status = PendingCut
			exec := t
			if n.cfg.inFriendly(t) {
				exec = n.cfg.friendlyEnd(t)
			}
			n.cutAt = exec
			n.emit(Event{Kind: EvPendingCut, At: t, ExecuteAt: exec, Balance: n.balance})
		case PendingRestore:
			n.status = Cut
			n.cutSince = t
			n.restoreAt = 0
			n.emit(Event{Kind: EvRestoreCanceled, At: t, Balance: n.balance})
		}
	} else if n.status == PendingCut {
		n.status = Powered
		n.cutAt = 0
		n.emit(Event{Kind: EvCutCanceled, At: t, Balance: n.balance})
	}
	if n.status == PendingRestore && n.balance < n.cfg.RestoreThreshold {
		n.status = Cut
		n.cutSince = t
		n.restoreAt = 0
		n.emit(Event{Kind: EvRestoreCanceled, At: t, Balance: n.balance})
	}
	if n.status == PendingCut && n.cutAt <= t {
		n.doCut(t)
	}
}

func (n *Naive) Advance(t Tick) ([]Event, error) {
	if err := n.checkTime(t); err != nil {
		return nil, err
	}
	return n.advance(t), nil
}

func (n *Naive) AddReading(t Tick, cumulative int64) ([]Event, error) {
	if err := n.checkTime(t); err != nil {
		return nil, err
	}
	if n.hasReading && t <= n.lastReadingAt {
		return nil, ErrOrder
	}
	if n.hasReading && cumulative < n.lastCum {
		return nil, ErrReadingBack
	}
	from := len(n.events)
	n.advance(t)
	n.charge(t, cumulative)
	return n.events[from:], nil
}

func (n *Naive) EnableEmergency(t Tick) ([]Event, error) {
	if err := n.checkTime(t); err != nil {
		return nil, err
	}
	willBeCut := n.status == Cut ||
		(n.status == PendingCut && n.cutAt <= t) ||
		(n.status == PendingRestore && n.restoreAt <= t)
	if willBeCut {
		return nil, ErrStateForbidden
	}
	if n.emgCycle {
		return nil, ErrAlreadyUsed
	}
	if n.balance >= n.cfg.EmergencyThreshold {
		return nil, ErrNotEligible
	}
	from := len(n.events)
	n.advance(t)
	g := n.cfg.EmergencyAmount
	n.balance += g
	n.emergencyUsed += g
	n.sumGrant += g
	n.emgCycle = true
	n.emit(Event{Kind: EvEmergencyGranted, At: t, Granted: g, Balance: n.balance})
	if n.status == PendingCut {
		if n.balance >= 0 {
			n.status = Powered
			n.cutAt = 0
			n.emit(Event{Kind: EvCutCanceled, At: t, Balance: n.balance})
		} else {
			n.scheduleCut(t)
		}
	}
	n.updateWarn(t)
	return n.events[from:], nil
}

func (n *Naive) Recharge(t Tick, amount Money) ([]Event, error) {
	if amount <= 0 {
		return nil, ErrInvalidParam
	}
	if err := n.checkTime(t); err != nil {
		return nil, err
	}
	from := len(n.events)
	n.advance(t)
	emgPay := amount
	if emgPay > n.emergencyUsed {
		emgPay = n.emergencyUsed
	}
	rest := amount - emgPay
	r := n.cfg.DebtRepayRatio
	debtPay := floorDiv(rest*r.N, r.D)
	if debtPay > n.debt {
		debtPay = n.debt
	}
	toBal := rest - debtPay
	n.sumRecharge += amount
	n.sumRepaid += emgPay
	n.emergencyUsed -= emgPay
	n.debt -= debtPay
	n.balance += toBal
	n.emit(Event{Kind: EvRecharge, At: t, Recharge: amount,
		EmergencyPaid: emgPay, DebtPaid: debtPay, ToBalance: toBal,
		Balance: n.balance})
	if n.status == PendingCut && n.balance >= 0 {
		n.status = Powered
		n.cutAt = 0
		n.emit(Event{Kind: EvCutCanceled, At: t, Balance: n.balance})
	}
	if n.status == Cut || n.status == PendingRestore {
		if n.balance >= n.cfg.RestoreThreshold {
			n.status = PendingRestore
			n.restoreAt = t + n.cfg.ConfirmWindow
			n.emit(Event{Kind: EvPendingRestore, At: t,
				Deadline: n.restoreAt, Balance: n.balance})
		} else if n.status == PendingRestore {
			n.status = Cut
			n.cutSince = t
			n.restoreAt = 0
			n.emit(Event{Kind: EvRestoreCanceled, At: t, Balance: n.balance})
		}
	}
	n.updateWarn(t)
	return n.events[from:], nil
}

func (n *Naive) ConfirmRestore(t Tick) ([]Event, error) {
	if err := n.checkTime(t); err != nil {
		return nil, err
	}
	if n.status == PendingRestore {
		if t > n.restoreAt {
			return nil, ErrConfirmTimeout
		}
	} else {
		return nil, ErrStateForbidden
	}
	from := len(n.events)
	n.advance(t)
	if n.status != PendingRestore {
		return nil, ErrConfirmTimeout
	}
	n.status = Powered
	n.restoreAt = 0
	n.emit(Event{Kind: EvRestored, At: t, Balance: n.balance})
	n.updateWarn(t)
	return n.events[from:], nil
}

func (n *Naive) AddTariff(start Tick, price int64) error {
	if price < 0 || start < n.cfg.CreatedAt {
		return ErrInvalidParam
	}
	if start < n.now {
		return ErrClockBack
	}
	if len(n.cfg.Tariffs) > 0 && start <= n.cfg.Tariffs[len(n.cfg.Tariffs)-1].Start {
		return ErrInvalidParam
	}
	n.cfg.Tariffs = append(n.cfg.Tariffs, Tariff{Start: start, Price: price})
	return nil
}

func (n *Naive) SetWarnThreshold(v Money) error {
	if v <= 0 {
		return ErrInvalidParam
	}
	n.cfg.WarnThreshold = v
	return nil
}

func (n *Naive) Snapshot() Snapshot {
	cut := n.cycCut
	if n.status == Cut {
		cut += n.now - n.cutSince
	}
	evs := append([]Event(nil), n.events...)
	return Snapshot{
		Now: n.now, Status: n.status, Balance: n.balance, Debt: n.debt,
		EmergencyUsed: n.emergencyUsed, CutExecuteAt: n.cutAt,
		RestoreDeadline: n.restoreAt, Warned: n.warned,
		EmergencyEnabledThisCycle: n.emgCycle, CycleIndex: n.cycle,
		TotalRecharge: n.sumRecharge, TotalEmergencyGranted: n.sumGrant,
		TotalDeducted: n.sumDeduct, TotalEmergencyRepaid: n.sumRepaid,
		CycleDeducted: n.cycDeduct, CycleWarns: n.cycWarns,
		CycleCutDuration: cut, Events: evs,
	}
}
