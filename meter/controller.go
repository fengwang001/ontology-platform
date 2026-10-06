package meter

// advanceLocked 把时钟推进到 t，按 tick 顺序处理所有到期效果：
// 周期边界（先汇总再重置资格）→ 停电执行 → 复电确认超时。
// 停电时长在每个周期边界处切分，跨周期停电各自计入。
// 所有效果按时刻顺序处理；同一时刻周期边界先于停电执行，
// 停电执行先于复电超时（二者状态互斥，不会真正同时发生）。
func (c *Controller) emitLocked(e Event) {
	c.events = append(c.events, e)

}

func (c *Controller) flushCutDurationLocked(at Tick) {
	if c.status == Cut {
		c.cycleCutDuration += at - c.cutAccumStart
		c.cutAccumStart = at
	}
}

func (c *Controller) closeCycleLocked(boundary Tick) {
	c.flushCutDurationLocked(boundary)
	start, end := c.cfg.cycleBounds(c.cycleIndex)
	c.emitLocked(Event{
		Kind:        EvCycleSummary,
		At:          boundary,
		Cycle:       c.cycleIndex,
		CycleStart:  start,
		CycleEnd:    end,
		Deducted:    c.cycleDeducted,
		Warns:       c.cycleWarns,
		CutDuration: c.cycleCutDuration,
	})
	c.cycleIndex++
	c.cycleDeducted = 0
	c.cycleWarns = 0
	c.cycleCutDuration = 0
	c.emergencyUsedCycle = false
}

func (c *Controller) executeCutLocked(at Tick) {
	c.flushCutDurationLocked(at)
	c.debt += -c.balance
	c.balance = 0
	c.status = Cut
	c.cutExecuteAt = 0
	c.cutAccumStart = at
	c.emitLocked(Event{Kind: EvCut, At: at, Balance: 0, Debt: c.debt})
}

func (c *Controller) timeoutRestoreLocked(at Tick) {
	c.status = Cut
	c.cutAccumStart = at
	c.offerDeadline = 0
	c.restoreDeadline = 0
	c.emitLocked(Event{Kind: EvRestoreTimeout, At: at, Balance: c.balance})
}

// advanceUntilLocked 推进时钟并处理所有不晚于 t 的到期效果。
// 同一时刻按 周期边界 → 停电执行 → 复电超时 的固定顺序。
func (c *Controller) advanceUntilLocked(t Tick) []Event {
	from := len(c.events)
	for c.now < t {
		// 选出严格晚于当前时刻、且不晚于 t 的最近效果时刻；
		// 没有则直接推进到 t。
		next := t
		boundary, _ := c.cfg.cycleBounds(c.cycleIndex + 1)
		if boundary > c.now && boundary <= next {
			next = boundary
		}
		if c.status == PendingCut && c.cutExecuteAt > c.now &&
			c.cutExecuteAt <= next {
			next = c.cutExecuteAt
		}
		// 目标时刻严格晚于时限时，超时时刻须被选为中间步；
		// 目标时刻恰为时限时不选（该时刻仍可确认）。
		if c.status == PendingRestore && c.offerDeadline > c.now &&
			c.offerDeadline <= next && next > c.offerDeadline {
			next = c.offerDeadline
		}
		c.now = next
		// 固定顺序处理该时刻效果：周期边界 → 停电执行 → 复电超时。
		if b, _ := c.cfg.cycleBounds(c.cycleIndex + 1); b == next && next > c.cfg.CreatedAt {
			c.closeCycleLocked(next)
		}
		if c.status == PendingCut && c.cutExecuteAt == next {
			c.executeCutLocked(next)
		}
		// 复电超时：推进途中（next<t）到达时限即处理；
		// 若用户调用时刻恰为时限（next==t），仍允许在该时刻确认。
		if c.status == PendingRestore && c.offerDeadline == next && next < t {
			c.timeoutRestoreLocked(next)
		}
		if next == t {
			break
		}
	}
	return c.events[from:]
}

func (c *Controller) chargeLocked(t Tick, cumulative int64) {
	if c.lastReading == nil {
		// 首条读数只登记，不计费。
		c.lastReading = &Reading{At: t, Cumulative: cumulative}
		return
	}
	energy := cumulative - c.lastReading.Cumulative
	prevAt := c.lastReading.At
	c.lastReading = &Reading{At: t, Cumulative: cumulative}
	if energy <= 0 {
		return
	}
	// amount 必须基于区间增量 energy 计算，不能使用累计读数。

	// 区间 [prevAt, t) 按区间起点时刻生效的电价计费，向下取整。
	price := c.cfg.priceAt(prevAt)
	amount := floorDiv(energy*price, 1000)
	wasCut := c.status == Cut
	if wasCut {
		// 停电期间用电照常计费：金额计入欠费，余额保持归零。
		c.debt += amount
		c.totalDeducted += amount
		c.cycleDeducted += amount
		c.emitLocked(Event{
			Kind:             EvDuringCutUsage,
			At:               t,
			CumulativeBefore: cumulative - energy,
			Energy:           energy,
			Price:            price,
			Amount:           amount,
			Balance:          c.balance,
			Debt:             c.debt,
		})
		return
	}
	c.balance -= amount
	c.totalDeducted += amount
	c.cycleDeducted += amount

	c.emitLocked(Event{
		Kind:             EvDeduct,
		At:               t,
		CumulativeBefore: cumulative - energy,
		Energy:           energy,
		Price:            price,
		Amount:           amount,
		Balance:          c.balance,
	})

	// 预警：由不低于阈值变为低于，一次性；阈值变化不触发。
	if c.balance < c.cfg.WarnThreshold {
		if !c.warned {
			c.warned = true
			c.cycleWarns++
			c.emitLocked(Event{Kind: EvWarn, At: t, Balance: c.balance})
		}
	} else {
		c.warned = false
	}

	// 扣费后为负：送电则排入停电；待停电维持原执行时刻；
	// 待复电期间再次跌破则取消待复电；余额非负则解除等待。
	if c.balance < 0 {
		switch c.status {
		case Powered:
			exec := t
			if c.cfg.inFriendly(t) {
				exec = c.cfg.friendlyEnd(t)
			}
			c.status = PendingCut
			c.cutExecuteAt = exec
			c.emitLocked(Event{
				Kind:      EvPendingCut,
				At:        t,
				ExecuteAt: exec,
				Balance:   c.balance,
			})
		case PendingRestore:
			c.status = Cut
			c.cutAccumStart = t
			c.restoreDeadline = 0
			c.offerDeadline = 0
			c.emitLocked(Event{Kind: EvRestoreCanceled, At: t, Balance: c.balance})
		}
	} else if c.status == PendingCut {
		c.status = Powered
		c.cutExecuteAt = 0
		c.emitLocked(Event{Kind: EvCutCanceled, At: t, Balance: c.balance})
	}
	if c.status == PendingRestore && c.balance < c.cfg.RestoreThreshold {
		c.status = Cut
		c.cutAccumStart = t
		c.restoreDeadline = 0
		c.offerDeadline = 0
		c.emitLocked(Event{Kind: EvRestoreCanceled, At: t, Balance: c.balance})
	}
	if c.status == PendingCut && c.cutExecuteAt <= t {
		c.executeCutLocked(t)
	}
}

// scheduleCutLocked 依据当前时刻重新判定停电执行时刻（友好时段推迟）。
func (c *Controller) scheduleCutLocked(at Tick) {
	exec := at
	if c.cfg.inFriendly(at) {
		exec = c.cfg.friendlyEnd(at)
	}
	c.cutExecuteAt = exec
	if exec <= at {
		c.executeCutLocked(at)
	}
}

func (c *Controller) updateWarnLocked(at Tick) {
	if c.balance < c.cfg.WarnThreshold {
		if !c.warned {
			c.warned = true
			c.cycleWarns++
			c.emitLocked(Event{Kind: EvWarn, At: at, Balance: c.balance})
		}
	} else {
		c.warned = false
	}
}

func (c *Controller) advanceLocked(t Tick) []Event {
	return nil
}
