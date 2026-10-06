package prepaid

// advanceTo 把时钟从当前时刻推进到 T（T >= 当前时刻），
// 按时间顺序依次处理途中每个到期的状态迁移与周期边界。
// 同一时刻既有状态迁移又有周期边界时，先处理状态迁移。
func (a *Account) advanceTo(T int64) {
	for {
		candT, candWhat := int64(-1), 0
		if a.state == StatePendingCutoff && a.cutoffExecAt <= T {
			candT, candWhat = a.cutoffExecAt, 1
		}
		if a.state == StatePendingRestore && a.restoreDeadline < T {
			if candT == -1 || a.restoreDeadline < candT {
				candT, candWhat = a.restoreDeadline, 2
			}
		}
		b := (a.clock/a.cfg.PeriodLength + 1) * a.cfg.PeriodLength
		if b <= T && (candT == -1 || b < candT) {
			candT, candWhat = b, 3
		}
		if candT == -1 {
			break
		}
		a.clock = candT
		switch candWhat {
		case 1:
			a.executeCutoff()
		case 2:
			a.expireRestore()
		case 3:
			a.crossBoundary()
		}
	}
	a.clock = T
}

// executeCutoff 在停电执行时刻转为已停电：
// 把此时的负余额转为欠费、余额归零。
func (a *Account) executeCutoff() {
	a.state = StateCutOff
	if a.balance < 0 {
		a.arrears += -a.balance
		a.balance = 0
	}
	a.cutoffSince = a.clock
	a.emit(Event{Kind: EvCutoffExecuted, Time: a.clock, Balance: a.balance, Arrears: a.arrears})
}

// expireRestore 复电确认超时：回到已停电，须再次达到条件才重新进入待复电。
func (a *Account) expireRestore() {
	a.state = StateCutOff
	a.cutoffSince = a.clock
	a.emit(Event{Kind: EvRestoreExpired, Time: a.clock, Balance: a.balance, Arrears: a.arrears})
}

// crossBoundary 处理一次周期边界（当前时钟已位于边界上）：
// 切分跨周期的停电时长，记录周期汇总并重置周期计数器。
// 应急启用资格按周期号比较，跨边界后自动失效，无需显式重置。
func (a *Account) crossBoundary() {
	b := a.clock
	if a.state == StateCutOff {
		a.periodCutoffDur += b - a.cutoffSince
		a.cutoffSince = b
	}
	a.emit(Event{
		Kind:           EvPeriodSummary,
		Time:           b,
		Period:         b/a.cfg.PeriodLength - 1,
		Charges:        a.periodCharges,
		Warnings:       a.periodWarnings,
		CutoffDuration: a.periodCutoffDur,
	})
	a.periodCharges, a.periodWarnings, a.periodCutoffDur = 0, 0, 0
}
