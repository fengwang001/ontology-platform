package meter

func New(cfg Config, initialBalance Money) *Controller {
	if err := cfg.validate(initialBalance); err != nil {
		return nil
	}
	c := &Controller{
		cfg:        cfg,
		now:        cfg.CreatedAt,
		status:     Powered,
		balance:    initialBalance,
		warned:     initialBalance < cfg.WarnThreshold,
		cycleIndex: 0,
		// 开户余额视作首次入账，使资金恒等式自创建起成立。
		totalRecharge: initialBalance,
	}
	c.emitLocked(Event{Kind: EvCreated, At: cfg.CreatedAt, Balance: initialBalance})
	return c
}

// checkTimeLocked 参数非法（时刻早于创建）> 时钟回退（时刻早于当前）。
func (c *Controller) checkTimeLocked(t Tick) error {
	if t < c.cfg.CreatedAt {
		return ErrInvalidParam
	}
	if t < c.now {
		return ErrClockBack
	}
	return nil
}

func (c *Controller) Advance(t Tick) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(t); err != nil {
		return nil, err
	}
	return c.advanceUntilLocked(t), nil
}

func (c *Controller) AddReading(t Tick, cumulative int64) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(t); err != nil {
		return nil, err
	}
	if c.lastReading != nil && t <= c.lastReading.At {
		return nil, ErrOrder
	}
	if c.lastReading != nil && cumulative < c.lastReading.Cumulative {
		return nil, ErrReadingBack
	}
	from := len(c.events)
	c.advanceUntilLocked(t)
	c.chargeLocked(t, cumulative)
	return c.events[from:], nil
}

func (c *Controller) EnableEmergency(t Tick) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(t); err != nil {
		return nil, err
	}
	from := len(c.events)
	// 投影推进后的状态，在不改动时钟的前提下完成拒绝判定，
	// 保证任何被拒绝操作不改变状态与时钟。
	willBeCut := c.status == Cut ||
		(c.status == PendingCut && c.cutExecuteAt <= t) ||
		(c.status == PendingRestore && c.offerDeadline <= t)
	// 拒绝次序：状态不允许 > 本周期已启用 > 未达启用条件。
	if willBeCut {
		return nil, ErrStateForbidden
	}
	if c.emergencyUsedCycle {
		return nil, ErrAlreadyUsed
	}
	if c.balance >= c.cfg.EmergencyThreshold {
		return nil, ErrNotEligible
	}

	c.advanceUntilLocked(t)
	grant := c.cfg.EmergencyAmount
	c.balance += grant
	c.emergencyUsed += grant
	c.totalEmergencyGranted += grant
	c.emergencyUsedCycle = true
	c.emitLocked(Event{
		Kind:    EvEmergencyGranted,
		At:      t,
		Granted: grant,
		Balance: c.balance,
	})

	if c.status == PendingCut {
		if c.balance >= 0 {
			c.status = Powered
			c.cutExecuteAt = 0
			c.emitLocked(Event{Kind: EvCutCanceled, At: t, Balance: c.balance})
		} else {
			c.scheduleCutLocked(t)
		}
	}
	c.updateWarnLocked(t)
	return c.events[from:], nil
}

func (c *Controller) Recharge(t Tick, amount Money) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if amount <= 0 {
		return nil, ErrInvalidParam
	}
	if err := c.checkTimeLocked(t); err != nil {
		return nil, err
	}
	from := len(c.events)
	c.advanceUntilLocked(t)

	// 三段分配：先还应急额度；剩余按比例（向下取整）清偿欠费；其余入余额。
	emergencyPaid := amount
	if emergencyPaid > c.emergencyUsed {
		emergencyPaid = c.emergencyUsed
	}
	rest := amount - emergencyPaid
	r := c.cfg.DebtRepayRatio
	debtPart := floorDiv(rest*r.N, r.D)
	if debtPart > c.debt {
		debtPart = c.debt
	}
	toBalance := rest - debtPart

	c.totalRecharge += amount
	c.totalEmergencyRepaid += emergencyPaid
	c.emergencyUsed -= emergencyPaid
	c.debt -= debtPart
	c.balance += toBalance
	c.emitLocked(Event{
		Kind:          EvRecharge,
		At:            t,
		Recharge:      amount,
		EmergencyPaid: emergencyPaid,
		DebtPaid:      debtPart,
		ToBalance:     toBalance,
		Balance:       c.balance,
	})

	if c.status == PendingCut && c.balance >= 0 {
		c.status = Powered
		c.cutExecuteAt = 0
		c.emitLocked(Event{Kind: EvCutCanceled, At: t, Balance: c.balance})
	}
	if c.status == Cut || c.status == PendingRestore {
		if c.balance >= c.cfg.RestoreThreshold {
			c.status = PendingRestore
			c.restoreDeadline = t + c.cfg.ConfirmWindow
			c.offerDeadline = c.restoreDeadline
			c.emitLocked(Event{
				Kind:     EvPendingRestore,
				At:       t,
				Deadline: c.restoreDeadline,
				Balance:  c.balance,
			})
		} else if c.status == PendingRestore {
			c.status = Cut
			c.cutAccumStart = t
			c.restoreDeadline = 0
			c.offerDeadline = 0
			c.emitLocked(Event{Kind: EvRestoreCanceled, At: t, Balance: c.balance})
		}
	}
	c.updateWarnLocked(t)
	return c.events[from:], nil
}

func (c *Controller) ConfirmRestore(t Tick) ([]Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(t); err != nil {
		return nil, err
	}
	// 待复电状态下调用确认：调用时刻已晚于时限按确认超时拒绝；
	// 其他状态按状态不允许拒绝（拒绝次序：状态不允许 > 确认超时）。
	if c.status == PendingRestore {
		if t > c.offerDeadline {
			return nil, ErrConfirmTimeout
		}
	} else {
		return nil, ErrStateForbidden
	}
	c.advanceUntilLocked(t)
	from := len(c.events)
	c.status = Powered
	c.restoreDeadline = 0
	c.offerDeadline = 0
	c.emitLocked(Event{Kind: EvRestored, At: t, Balance: c.balance})
	c.updateWarnLocked(t)
	return c.events[from:], nil
}

func (c *Controller) AddTariff(start Tick, price int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if price < 0 || start < c.cfg.CreatedAt {
		return ErrInvalidParam
	}
	if start < c.now {
		return ErrClockBack
	}
	if n := len(c.cfg.Tariffs); n > 0 && start <= c.cfg.Tariffs[n-1].Start {
		return ErrInvalidParam
	}
	c.cfg.Tariffs = append(c.cfg.Tariffs, Tariff{Start: start, Price: price})
	return nil
}

func (c *Controller) SetWarnThreshold(v Money) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v <= 0 {
		return ErrInvalidParam
	}
	c.cfg.WarnThreshold = v
	return nil
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutDuration := c.cycleCutDuration
	if c.status == Cut {
		cutDuration += c.now - c.cutAccumStart
	}
	evs := make([]Event, len(c.events))
	copy(evs, c.events)
	return Snapshot{
		Now:                       c.now,
		Status:                    c.status,
		Balance:                   c.balance,
		Debt:                      c.debt,
		EmergencyUsed:             c.emergencyUsed,
		CutExecuteAt:              c.cutExecuteAt,
		RestoreDeadline:           c.restoreDeadline,
		Warned:                    c.warned,
		EmergencyEnabledThisCycle: c.emergencyUsedCycle,
		CycleIndex:                c.cycleIndex,
		TotalRecharge:             c.totalRecharge,
		TotalEmergencyGranted:     c.totalEmergencyGranted,
		TotalDeducted:             c.totalDeducted,
		TotalEmergencyRepaid:      c.totalEmergencyRepaid,
		CycleDeducted:             c.cycleDeducted,
		CycleWarns:                c.cycleWarns,
		CycleCutDuration:          cutDuration,
		Events:                    evs,
	}
}
