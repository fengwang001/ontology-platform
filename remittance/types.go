// Package remittance 实现跨境汇款的报价锁汇、限额占用与合规审核。
//
// 时间约定：所有时间均为整数秒（Unix 秒）。自然日序号为 now/86400 向下取整。
// 滚动年度指占用日序号与当前日序号之差小于 365 的全部占用。
package remittance

// Config 为系统级参数。所有额度均以目标币种最小单位计。
type Config struct {
	SingleLimit     int64 // 单笔限额
	DayLimit        int64 // 自然日限额
	YearLimit       int64 // 滚动年度额度
	QuoteTTL        int64 // T：报价有效期秒数，到期时刻恰等仍有效
	ReviewThreshold int64 // 审核阈值：目标额 >= 阈值进入待审核
	ReviewTimeout   int64 // R：审核时限秒数，提交起 R 秒内（含第 R 秒）须批准
}

// Status 为汇款生命周期状态。
type Status int

const (
	StatusPendingReview Status = iota // 待审核：已占用额度，未出款
	StatusSucceeded                   // 已出款
	StatusFailed                      // 失败（拒绝/撤回/审核逾期），占用已释放
)

func (s Status) String() string {
	switch s {
	case StatusPendingReview:
		return "PendingReview"
	case StatusSucceeded:
		return "Succeeded"
	case StatusFailed:
		return "Failed"
	}
	return "Unknown"
}

// SubmitResult 为一次被接受提交的结果快照（幂等重放时原样返回）。
type SubmitResult struct {
	RemittanceID string
	TargetAmount int64 // 目标额：收款人实际入账额（向下取整）
	HoldAmount   int64 // 占用额（向上取整）
	Status       Status
}

// Usage 为某汇款人在查询时刻的额度占用视图。
type Usage struct {
	DayIndex        int64 // 查询时刻的自然日序号
	DayUsed         int64 // 当日已占用
	RollingYearUsed int64 // 滚动年度已占用
}

// RemittanceView 为汇款记录的对外视图。
type RemittanceView struct {
	ID           string
	Remitter     string
	Payee        string
	QuoteID      string
	TargetAmount int64
	HoldAmount   int64
	DayIndex     int64
	SubmitNow    int64
	Deadline     int64 // 审核截止时刻（SubmitNow+R），非审核类汇款无意义
	Status       Status
}

const (
	microMillion  = int64(1_000_000) // 汇率分母：百万分之几
	secondsPerDay = int64(86_400)
	rollingDays   = int64(365)
)

// dayIndex 由秒级时间求自然日序号（now 已保证非负）。
func dayIndex(now int64) int64 { return now / secondsPerDay }
