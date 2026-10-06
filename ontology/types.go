package ffm

// 本文件定义常旅客里程系统的对外类型与错误分类。
// 所有时刻均为非负整数秒（相对账户开通时刻或 Unix 秒均可，系统只做差与比较），
// 所有里程均为整数 int64。

// ErrCode 是统一的拒绝类别编码，次序即题目规定的拒绝优先级。
type ErrCode int

const (
	// ErrInvalidParam 参数非法（最优先）。
	ErrInvalidParam ErrCode = iota + 1
	// ErrClockBack 操作时刻早于上一次被接受操作的时刻。
	ErrClockBack
	// ErrAccountNotExist 账户不存在。
	ErrAccountNotExist
	// ErrAccountFrozen 账户因不活跃被冻结。
	ErrAccountFrozen
	// ErrDuplicatePosting 同一航段重复入账（含已退票航段再入账）。
	ErrDuplicatePosting
	// ErrLatePosting 超过补登窗口。
	ErrLatePosting
	// ErrRedemptionNotExist 兑换记录不存在。
	ErrRedemptionNotExist
	// ErrRedemptionCancelled 兑换记录已取消。
	ErrRedemptionCancelled
	// ErrInsufficientMiles 可兑换里程不足（或有欠账）。
	ErrInsufficientMiles
)

// Error 实现 error，消息为稳定中文结论，便于复现与断言。
type Error struct {
	code ErrCode
	msg  string
}

func (e *Error) Error() string { return e.msg }

// Code 返回拒绝类别。
func (e *Error) Code() ErrCode { return e.code }

func errWith(code ErrCode, msg string) *Error { return &Error{code: code, msg: msg} }

// Config 为全系统固定配置；同一 System 内所有账户共用。
type Config struct {
	// RetroWindow 飞行后允许补登入账的窗口长度（秒），窗口终点取闭。
	RetroWindow int64
	// MinMiles 最低保底基础里程。
	MinMiles int64
	// InactiveDuration 不活跃冻结时长（秒），差值 >= 该值即冻结。
	InactiveDuration int64
	// CancelFee 取消兑换的手续里程（不退还）。
	CancelFee int64
	// PeriodLength 定级周期固定长度（秒，正整数）。
	PeriodLength int64
	// Thresholds 三个递增的升级门槛定级里程。
	Thresholds [3]int64
	// Bonuses 四个等级对应的加成百分比（如 20 表示 +20%）。
	Bonuses [4]int64
}

// Segment 描述一个待入账航段。
type Segment struct {
	// ID 唯一航段标识。
	ID string
	// Distance 航距（正整数）。
	Distance int64
	// Rate 舱位折算比例，0..300 的百分数（如 150 表示 150%）。
	Rate       int64
	FlightTime int64
}

// PostResult 是入账成功的确定性结果。
type PostResult struct {
	Base        int64 // 基础里程（含保底）
	Bonus       int64 // 本次等级加成
	Redeemable  int64 // 实际增加的可兑换里程（已考虑欠账抵扣）
	ToDebt      int64 // 本次入账用于抵扣欠账的里程
	TierBefore  int   // 入账前等级（0..3）
	TierAfter   int   // 入账后等级（0..3）
	PeriodIndex int64 // 航段所属定级周期序号（从 0 起）
}

// RedeemResult 是兑换成功结果。
type RedeemResult struct {
	RecordID string
	Spent    int64
	Balance  int64
}

// CancelResult 是取消兑换成功结果。
type CancelResult struct {
	Refunded int64 // 实际退回余额的里程
	ToDebt   int64 // 用于抵扣欠账的里程
	Balance  int64
	Debt     int64
}

// Snapshot 是账户对外的 O(1) 状态结论。
type Snapshot struct {
	Tier       int   // 当前等级 0..3
	Qualifying int64 // 当前（最近一个已开始）周期累计定级里程
	Redeemable int64 // 可兑换里程余额（恒非负）
	Debt       int64 // 欠账
	LastOpTime int64 // 上一次被接受操作的时刻
	ActiveTime int64 // 最近一次被接受的入账或兑换时刻（活跃锚点）
}
