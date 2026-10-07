package payledger

// AuthStatus 授权在查询时刻的状态。
type AuthStatus int

const (
	// StatusActive 有效（未终结且未过期）。
	StatusActive AuthStatus = iota
	// StatusExpired 已过期（超过有效期截止日，持有已自动失效）。
	StatusExpired
	// StatusVoided 已撤销。
	StatusVoided
	// StatusFinalCaptured 已终捕。
	StatusFinalCaptured
)

func (s AuthStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusExpired:
		return "expired"
	case StatusVoided:
		return "voided"
	case StatusFinalCaptured:
		return "final_captured"
	}
	return "unknown"
}

// AuthSnapshot 授权在指定 now 下的只读快照。查询不修改任何状态。
type AuthSnapshot struct {
	AuthID        string
	AccountID     string
	Status        AuthStatus
	RemainingHold int64 // 剩余持有（查询 now 时刻）
	CumAuth       int64 // 累计授权额
	Captured      int64 // 累计捕获额
	Refunded      int64 // 累计退款额
	ExpiryDay     int64 // 有效期截止日（含当天）
}

// Config 账本参数。
type Config struct {
	// ExpiryDays 授权有效期天数 E：创建/增量日起第 E 天（含）内有效。
	ExpiryDays int64
	// ToleranceBps 捕获容差基点（万分之一）。累计捕获上限 =
	// 累计授权额 + floor(累计授权额 * ToleranceBps / 10000)。
	ToleranceBps int64
}
