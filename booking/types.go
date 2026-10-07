package booking

import (
	"errors"
	"fmt"
)

// Code 是操作结果的错误码，按固定优先次序检查：
// 参数非法 < 时钟回退 < 不存在 < 状态不允许 < 日期不可订（封锁/冲突/间隙/最短入住/孤夜）。
type Code int

const (
	CodeOK Code = iota
	CodeInvalidParams
	CodeClockRollback
	CodeNotFound
	CodeInvalidState
	CodeBlocked
	CodeConflict
	CodeGap
	CodeMinStay
	CodeOrphan
)

func (c Code) String() string {
	switch c {
	case CodeOK:
		return "OK"
	case CodeInvalidParams:
		return "INVALID_PARAMS"
	case CodeClockRollback:
		return "CLOCK_ROLLBACK"
	case CodeNotFound:
		return "NOT_FOUND"
	case CodeInvalidState:
		return "INVALID_STATE"
	case CodeBlocked:
		return "UNAVAILABLE_BLOCKED"
	case CodeConflict:
		return "UNAVAILABLE_CONFLICT"
	case CodeGap:
		return "UNAVAILABLE_GAP"
	case CodeMinStay:
		return "UNAVAILABLE_MIN_STAY"
	case CodeOrphan:
		return "UNAVAILABLE_ORPHAN"
	}
	return fmt.Sprintf("Code(%d)", int(c))
}

// OpError 是唯一暴露的错误类型，只报告按次序检查到的第一个错误。
type OpError struct {
	Code Code
	Msg  string
}

func (e *OpError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func errf(c Code, format string, args ...any) *OpError {
	return &OpError{Code: c, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf 提取错误的 Code；err 为 nil 时返回 CodeOK。
func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return Code(-1)
}

// Status 是预订的生命周期状态。
type Status string

const (
	StatusHold      Status = "HOLD"
	StatusConfirmed Status = "CONFIRMED"
	StatusExpired   Status = "EXPIRED"
	StatusCancelled Status = "CANCELLED"
)

// Config 是服务级常量：H 为保留有效时刻单位数，P/Q 为退款阶梯天数。
type Config struct {
	HoldWindow     int // H：保留有效期（now <= 创建时刻+H 内可付款）
	RefundFullDays int // P：取消日距入住日不少于 P 天全额退款
	RefundHalfDays int // Q：不少于 Q 天且少于 P 天退一半
}

func (c Config) validate() error {
	if c.HoldWindow < 0 {
		return errf(CodeInvalidParams, "hold window must be >= 0, got %d", c.HoldWindow)
	}
	if c.RefundHalfDays < 1 || c.RefundFullDays <= c.RefundHalfDays {
		return errf(CodeInvalidParams, "refund tiers must satisfy P > Q >= 1, got P=%d Q=%d",
			c.RefundFullDays, c.RefundHalfDays)
	}
	return nil
}

// ListingCfg 是房源级设置。
type ListingCfg struct {
	NightlyPrice   int64 // 每夜价格（最小货币单位）
	DefaultMinStay int   // 默认最短入住夜数
	GapDays        int   // 换客间隙天数，0 表示允许退房日等于下一入住日
}

func (c ListingCfg) validate() error {
	if c.NightlyPrice < 0 {
		return errf(CodeInvalidParams, "nightly price must be >= 0, got %d", c.NightlyPrice)
	}
	if c.DefaultMinStay < 1 {
		return errf(CodeInvalidParams, "default min stay must be >= 1, got %d", c.DefaultMinStay)
	}
	if c.GapDays < 0 {
		return errf(CodeInvalidParams, "gap days must be >= 0, got %d", c.GapDays)
	}
	return nil
}

// Booking 是一笔预订（保留或已确认）。
type Booking struct {
	ID        string
	ListingID string
	Checkin   int
	Checkout  int
	Expiry    int // 保留有效期最后一刻（含）；已确认后无意义
	Status    Status
	Amount    int64 // 已付款金额，修改日期不改变
	Refund    int64 // 取消时记录的退款
	PriceDiff int64 // 修改日期产生的差价，只记录不结算
	Modified  bool  // 是否已修改过一次
}

// Block 是房东封锁区间 [Start, End)。
type Block struct {
	ID    string
	Start int
	End   int
}

// IntervalInfo 是日历上一个有效占用区间的快照。
type IntervalInfo struct {
	Start int
	End   int
	Kind  string // "booking" 或 "block"
	ID    string
}

// BookingInfo 是预订的快照。
type BookingInfo struct {
	ID        string
	Checkin   int
	Checkout  int
	Expiry    int
	Status    Status
	Amount    int64
	Refund    int64
	PriceDiff int64
	Modified  bool
}

// ListingSnapshot 是房源日历与预订的确定性快照，用于重放校验。
type ListingSnapshot struct {
	LastNow   int
	Intervals []IntervalInfo // 按起始日升序，互不相交
	Bookings  []BookingInfo  // 按 ID 升序
}
