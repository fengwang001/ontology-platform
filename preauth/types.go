package preauth

// Status 是授权的可观测状态，按查询所用 now 判定。
type Status int

const (
	StatusActive    Status = iota // 有效：未撤销、未终捕、now 未过有效期截止日
	StatusExpired                 // 已过期：超过有效期截止日
	StatusVoided                  // 已撤销
	StatusFinalized               // 已终捕
)

func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusExpired:
		return "expired"
	case StatusVoided:
		return "voided"
	case StatusFinalized:
		return "finalized"
	default:
		return "unknown"
	}
}

// AuthView 是授权在某个 now 下的只读视图。
type AuthView struct {
	ID         string // 全局唯一授权编号
	AccountID  string // 所属卡账户
	Status     Status // 当前状态
	Authorized int64  // 累计授权额（初始额 + 历次增量）
	Captured   int64  // 累计捕获额（含上浮部分）
	Refunded   int64  // 累计退款额
	Remaining  int64  // 当前剩余持有（仅 StatusActive 时可能为正）
	ExpiresDay int64  // 有效期截止日（含当天）
}

// AccountView 是卡账户在某个 now 下的只读视图。
type AccountView struct {
	ID        string
	Credit    int64 // 信用额度
	Posted    int64 // 已入账余额（捕获入账减退款）
	OnHold    int64 // 全部有效持有的合计
	Available int64 // 可用额度
	Now       int64 // 查询所用时间
}

// Config 是系统参数：有效期天数与容差基点（1bp = 0.01%）。
type Config struct {
	ValidityDays int64 // E：有效期为创建/增量日起 E 天（含第 E 天）
	ToleranceBPS int64 // 捕获上浮容差，单位基点；如 1000 表示 10%
}
