package dtc

// Config 为生命周期管理配置，全部阈值在 NewManager 时校验。
type Config struct {
	DebounceRiseStep  int // 去抖上升步长（每次失败上报）
	DebounceFailLimit int // 去抖失败判定线（>0）
	DebounceFallStep  int // 去抖下降步长（每次通过上报）
	DebouncePassLimit int // 去抖通过判定线（<0）
	ConfirmCycles     int // 确认所需的连续失败循环数
	HealWarmUpCycles  int // 愈合所需的连续无故障暖机循环数
	AutoClearWarmUps  int // 自动清除所需的无故障暖机循环数
	WarmUpTempRise    int // 暖机判定的冷却液温升阈值
	WarmUpFinalTemp   int // 暖机判定的冷却液终温阈值
}

// Validate 校验配置合法性。
func (c Config) Validate() error {
	if c.DebounceRiseStep <= 0 {
		return newError(ErrInvalidParam, "debounce rise step must be > 0, got %d", c.DebounceRiseStep)
	}
	if c.DebounceFallStep <= 0 {
		return newError(ErrInvalidParam, "debounce fall step must be > 0, got %d", c.DebounceFallStep)
	}
	if c.DebounceFailLimit <= 0 {
		return newError(ErrInvalidParam, "debounce fail limit must be > 0, got %d", c.DebounceFailLimit)
	}
	if c.DebouncePassLimit >= 0 {
		return newError(ErrInvalidParam, "debounce pass limit must be < 0, got %d", c.DebouncePassLimit)
	}
	if c.ConfirmCycles <= 0 {
		return newError(ErrInvalidParam, "confirm cycles must be > 0, got %d", c.ConfirmCycles)
	}
	if c.HealWarmUpCycles <= 0 {
		return newError(ErrInvalidParam, "heal warm-up cycles must be > 0, got %d", c.HealWarmUpCycles)
	}
	if c.AutoClearWarmUps < c.HealWarmUpCycles {
		return newError(ErrInvalidParam, "auto clear warm-ups (%d) must be >= heal warm-up cycles (%d)",
			c.AutoClearWarmUps, c.HealWarmUpCycles)
	}
	if c.WarmUpTempRise < 0 {
		return newError(ErrInvalidParam, "warm-up temp rise must be >= 0, got %d", c.WarmUpTempRise)
	}
	return nil
}
