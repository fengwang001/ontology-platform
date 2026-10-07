package payledger

// account 卡账户：信用额度、已入账余额，以及按过期日聚合的持有跟踪器。
type account struct {
	id      string
	credit  int64 // 信用额度
	posted  int64 // 已入账余额（捕获 - 退款）
	tracker holdTracker
}

// authorization 授权记录。过期不是存储状态，而是由 expiryDay 与查询 now 派生。
type authorization struct {
	id        string
	accountID string
	cumAuth   int64 // 累计授权额
	captured  int64 // 累计捕获额
	refunded  int64 // 累计退款额
	expiryDay int64 // 有效期截止日（含当天）
	terminal  AuthStatus
	active    bool // false 表示已终结（撤销/终捕）
}

// remainingHold 授权在 now 时刻的剩余持有。
// 定义：累计授权额减已捕获额，向下截断到 0（超额捕获后持有清零）。
func (a *authorization) remainingHold(now int64) int64 {
	if !a.active || now > a.expiryDay {
		return 0
	}
	if r := a.cumAuth - a.captured; r > 0 {
		return r
	}
	return 0
}

// statusAt 授权在 now 时刻的状态。
func (a *authorization) statusAt(now int64) AuthStatus {
	if !a.active {
		return a.terminal
	}
	if now > a.expiryDay {
		return StatusExpired
	}
	return StatusActive
}

// allowedCapture 累计捕获上限：累计授权额按容差基点上浮，上浮部分向下取整。
func allowedCapture(cumAuth, toleranceBps int64) int64 {
	return cumAuth + cumAuth*toleranceBps/10000
}
