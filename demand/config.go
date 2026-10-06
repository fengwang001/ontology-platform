package demand

// Config 为最大需量控制器的静态配置。
//
// 时间单位均为秒（绝对时刻为非负整数），能量单位为千瓦秒。
type Config struct {
	// ContractKW 合同需量（千瓦，正整数）。
	ContractKW int64
	// WindowSec 需量窗口长度（秒，正整数）。
	WindowSec int64
	// SlipSec 窗口滑差（秒，正整数），窗口长度必须是滑差的整数倍。
	SlipSec int64
	// MaxPowerKW 上报区间平均功率的物理上限（千瓦，正整数）。
	MaxPowerKW int64
}

// Validate 校验配置参数。
func (c Config) Validate() error {
	if c.ContractKW <= 0 {
		return errParam("合同需量必须为正整数，实际为 %d", c.ContractKW)
	}
	if c.WindowSec <= 0 {
		return errParam("窗口长度必须为正整数，实际为 %d", c.WindowSec)
	}
	if c.SlipSec <= 0 {
		return errParam("滑差必须为正整数，实际为 %d", c.SlipSec)
	}
	if c.WindowSec%c.SlipSec != 0 {
		return errParam("窗口长度 %d 必须是滑差 %d 的整数倍", c.WindowSec, c.SlipSec)
	}
	if c.MaxPowerKW <= 0 {
		return errParam("物理上限功率必须为正整数，实际为 %d", c.MaxPowerKW)
	}
	return nil
}

// OverlapCount 返回任一时刻互相重叠、尚未结束的窗口个数，即窗口长度/滑差。
func (c Config) OverlapCount() int64 { return c.WindowSec / c.SlipSec }
