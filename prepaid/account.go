package prepaid

import (
	"sort"
	"sync"
)

// Account 为预付费电表账户。全部公开方法持有同一把互斥锁，
// 并发调用等价于某个串行执行顺序。
type Account struct {
	mu  sync.Mutex
	cfg Config

	clock   int64
	balance int64 // 余额，可为负
	arrears int64 // 欠费
	state   State

	emergencyUsed          int64 // 应急额度已用量（未偿还部分）
	emergencyEnabledPeriod int64 // 最近一次启用应急额度的周期号，-1 表示从未启用

	hasReading   bool
	lastReadTime int64
	lastEnergy   int64

	priceTimes  []int64 // 电价生效时刻，严格递增
	priceValues []int64

	warned bool // 预警闩锁：余额低于阈值期间不再重复预警

	cutoffExecAt    int64 // 待停电的执行时刻
	cutoffSince     int64 // 已停电状态的起始时刻（用于停电时长统计）
	restoreDeadline int64 // 待复电的确认截止时刻

	periodCharges   int64 // 本周期的扣费总额
	periodWarnings  int64 // 本周期的预警次数
	periodCutoffDur int64 // 本周期已累计的停电时长

	totalRecharge         int64 // 充值总额
	totalEmergencyGranted int64 // 应急额度获得总额
	totalCharged          int64 // 扣费总额
	totalEmergencyRepaid  int64 // 已偿还的应急额度

	holidays map[int64]bool // 节假日（天序号），全天友好
	events   []Event
}

// NewAccount 创建账户。参数不满足约束时报「参数非法」。
func NewAccount(cfg Config) (*Account, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Account{
		cfg:                    cfg,
		state:                  StateSupplyOn,
		emergencyEnabledPeriod: -1,
		priceTimes:             []int64{0},
		priceValues:            []int64{cfg.InitialPriceMilli},
		holidays:               make(map[int64]bool),
	}, nil
}

func (a *Account) emit(e Event) { a.events = append(a.events, e) }

// priceAt 返回时刻 t 生效的电价（二分查找，与读数、事件总数无关）。
func (a *Account) priceAt(t int64) int64 {
	i := sort.Search(len(a.priceTimes), func(i int) bool { return a.priceTimes[i] > t }) - 1
	return a.priceValues[i]
}

// checkWarning 在余额变化后维护预警闩锁：
// 余额由不低于阈值变为低于时记一次预警；回到不低于阈值前不重复预警。
func (a *Account) checkWarning(t int64) {
	if a.balance < a.cfg.WarnThreshold {
		if !a.warned {
			a.warned = true
			a.periodWarnings++
			a.emit(Event{Kind: EvWarning, Time: t, Balance: a.balance, Arrears: a.arrears})
		}
	} else {
		a.warned = false
	}
}

// afterBalanceDrop 在一次扣费后检查停电触发与待复电取消。
func (a *Account) afterBalanceDrop(t int64) {
	a.checkWarning(t)
	switch a.state {
	case StateSupplyOn:
		if a.balance < 0 {
			a.state = StatePendingCutoff
			a.cutoffExecAt = a.deferCutoff(t)
			a.emit(Event{Kind: EvCutoffScheduled, Time: t, ExecTime: a.cutoffExecAt, Balance: a.balance, Arrears: a.arrears})
			a.advanceTo(a.clock) // 执行时刻为当前时刻时立即执行
		}
	case StatePendingRestore:
		if a.balance < a.cfg.RestoreThreshold {
			a.state = StateCutOff
			a.cutoffSince = t
			a.emit(Event{Kind: EvRestoreCancelled, Time: t, Balance: a.balance, Arrears: a.arrears})
		}
	}
}

// AddReading 登记一条（时刻、累计电量）读数。
// 相邻读数之间的电量按区间起点时刻生效的电价计费，金额向下取整。
func (a *Account) AddReading(t, energy int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < 0 || energy < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < a.clock {
		return newErr(ErrClockRegression)
	}
	if a.hasReading && t <= a.lastReadTime {
		return newErr(ErrTimeOrder)
	}
	if a.hasReading && energy < a.lastEnergy {
		return newErr(ErrReadingRollback)
	}
	a.advanceTo(t)
	if a.hasReading {
		delta := energy - a.lastEnergy
		if delta > 0 {
			price := a.priceAt(a.lastReadTime)
			amount := delta * price / 1000
			if amount > 0 {
				a.balance -= amount
				a.totalCharged += amount
				a.periodCharges += amount
				a.emit(Event{Kind: EvCharge, Time: t, Energy: delta, Amount: amount, Price: price, Balance: a.balance, Arrears: a.arrears})
			}
			if a.state == StateCutOff {
				a.emit(Event{Kind: EvOffGridUsage, Time: t, Energy: delta, Balance: a.balance, Arrears: a.arrears})
			}
			if amount > 0 {
				a.afterBalanceDrop(t)
			}
		}
	}
	a.hasReading = true
	a.lastReadTime = t
	a.lastEnergy = energy
	return nil
}

// Recharge 充值：先偿还应急额度已用量，剩余部分按配置比例
// （向下取整）清偿欠费直到欠费为零，其余入余额。
func (a *Account) Recharge(t, amount int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < 0 || amount <= 0 {
		return newErr(ErrInvalidParam)
	}
	if t < a.clock {
		return newErr(ErrClockRegression)
	}
	a.advanceTo(t)
	repay := min(amount, a.emergencyUsed)
	a.emergencyUsed -= repay
	a.totalEmergencyRepaid += repay
	rem := amount - repay
	pay := rem * a.cfg.ArrearsRatioNum / a.cfg.ArrearsRatioDen
	if pay > a.arrears {
		pay = a.arrears
	}
	a.arrears -= pay
	toBal := rem - pay
	a.balance += toBal
	a.totalRecharge += amount
	a.emit(Event{Kind: EvRecharge, Time: t, Amount: amount, RepayEmergency: repay, RepayArrears: pay, ToBalance: toBal, Balance: a.balance, Arrears: a.arrears})
	a.checkWarning(t)
	switch a.state {
	case StatePendingCutoff:
		if a.balance >= 0 {
			a.state = StateSupplyOn
			a.emit(Event{Kind: EvCutoffCancelled, Time: t, Balance: a.balance, Arrears: a.arrears})
		}
	case StateCutOff:
		if a.balance >= a.cfg.RestoreThreshold {
			a.periodCutoffDur += t - a.cutoffSince
			a.state = StatePendingRestore
			a.restoreDeadline = t + a.cfg.ConfirmTimeout
			a.emit(Event{Kind: EvPendingRestore, Time: t, ExecTime: a.restoreDeadline, Balance: a.balance, Arrears: a.arrears})
		}
	}
	return nil
}

// EnableEmergency 启用应急额度：余额低于启用阈值且本结算周期内
// 未启用过时，把配置的应急额度计入余额，并重新判定停电执行时刻。
func (a *Account) EnableEmergency(t int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < a.clock {
		return newErr(ErrClockRegression)
	}
	// 投影时刻 t 的生效状态用于拒绝判定（不产生任何副作用）。
	eff := a.state
	if eff == StatePendingCutoff && a.cutoffExecAt <= t {
		eff = StateCutOff
	}
	if eff == StatePendingRestore && a.restoreDeadline < t {
		eff = StateCutOff
	}
	if eff == StateCutOff || eff == StatePendingRestore {
		return newErr(ErrStateNotAllowed)
	}
	if a.emergencyEnabledPeriod == t/a.cfg.PeriodLength {
		return newErr(ErrAlreadyEnabled)
	}
	if a.balance >= a.cfg.EmergencyThreshold {
		return newErr(ErrConditionNotMet)
	}
	a.advanceTo(t)
	a.balance += a.cfg.EmergencyAmount
	a.emergencyUsed += a.cfg.EmergencyAmount
	a.totalEmergencyGranted += a.cfg.EmergencyAmount
	a.emergencyEnabledPeriod = t / a.cfg.PeriodLength
	a.emit(Event{Kind: EvEmergencyEnabled, Time: t, Amount: a.cfg.EmergencyAmount, Balance: a.balance, Arrears: a.arrears})
	a.checkWarning(t)
	if a.state == StatePendingCutoff {
		if a.balance >= 0 {
			a.state = StateSupplyOn
			a.emit(Event{Kind: EvCutoffCancelled, Time: t, Balance: a.balance, Arrears: a.arrears})
		} else {
			a.cutoffExecAt = a.deferCutoff(t)
			a.emit(Event{Kind: EvCutoffRescheduled, Time: t, ExecTime: a.cutoffExecAt, Balance: a.balance, Arrears: a.arrears})
			a.advanceTo(a.clock)
		}
	}
	return nil
}

// ConfirmRestore 确认复电：须在待复电状态且不超过确认截止时刻。
func (a *Account) ConfirmRestore(t int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < a.clock {
		return newErr(ErrClockRegression)
	}
	if a.state != StatePendingRestore {
		return newErr(ErrStateNotAllowed)
	}
	if t > a.restoreDeadline {
		return newErr(ErrConfirmTimeout)
	}
	a.advanceTo(t)
	a.state = StateSupplyOn
	a.emit(Event{Kind: EvRestored, Time: t, Balance: a.balance, Arrears: a.arrears})
	return nil
}

// AdvanceClock 推进时钟，依次处理跨越的周期边界与到期迁移。
func (a *Account) AdvanceClock(t int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < a.clock {
		return newErr(ErrClockRegression)
	}
	a.advanceTo(t)
	return nil
}

// SetPrice 登记电价变更，在指定时刻生效，不影响已计费区间。
func (a *Account) SetPrice(t, priceMilli int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < 0 || priceMilli <= 0 {
		return newErr(ErrInvalidParam)
	}
	if t < a.clock {
		return newErr(ErrClockRegression)
	}
	if t <= a.priceTimes[len(a.priceTimes)-1] {
		return newErr(ErrTimeOrder)
	}
	a.advanceTo(t)
	a.priceTimes = append(a.priceTimes, t)
	a.priceValues = append(a.priceValues, priceMilli)
	a.emit(Event{Kind: EvPriceSet, Time: t, Price: priceMilli, Balance: a.balance, Arrears: a.arrears})
	return nil
}

// SetWarnThreshold 变更预警阈值。阈值变更本身不触发预警。
func (a *Account) SetWarnThreshold(v int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v <= 0 {
		return newErr(ErrInvalidParam)
	}
	a.cfg.WarnThreshold = v
	return nil
}

// AddHoliday 登记一个节假日（天序号），该天全天为友好时段。
func (a *Account) AddHoliday(day int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if day < 0 {
		return newErr(ErrInvalidParam)
	}
	a.holidays[day] = true
	return nil
}

// Snapshot 为账户状态的一致性快照。
type Snapshot struct {
	Clock                 int64
	Balance               int64
	Arrears               int64
	EmergencyUsed         int64
	State                 State
	Warned                bool
	CutoffExecAt          int64
	RestoreDeadline       int64
	TotalRecharge         int64
	TotalEmergencyGranted int64
	TotalCharged          int64
	TotalEmergencyRepaid  int64
}

// Snapshot 返回账户状态的一致性快照。
func (a *Account) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Snapshot{
		Clock:                 a.clock,
		Balance:               a.balance,
		Arrears:               a.arrears,
		EmergencyUsed:         a.emergencyUsed,
		State:                 a.state,
		Warned:                a.warned,
		CutoffExecAt:          a.cutoffExecAt,
		RestoreDeadline:       a.restoreDeadline,
		TotalRecharge:         a.totalRecharge,
		TotalEmergencyGranted: a.totalEmergencyGranted,
		TotalCharged:          a.totalCharged,
		TotalEmergencyRepaid:  a.totalEmergencyRepaid,
	}
}

// Events 返回迄今产生的全部事件（拷贝）。
func (a *Account) Events() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Event, len(a.events))
	copy(out, a.events)
	return out
}

// CheckInvariant 校验资金守恒不变量：
// 充值总额 + 应急额度获得总额 == 扣费总额 + 当前余额 + 已偿还的应急额度 - 当前欠费。
func (a *Account) CheckInvariant() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalRecharge+a.totalEmergencyGranted ==
		a.totalCharged+a.balance+a.totalEmergencyRepaid-a.arrears
}
