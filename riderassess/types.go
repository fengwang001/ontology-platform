package riderassess

// 本文件集中放置跨模块共享的类型定义与纯函数（配置、错误、等级划分、周期换算）。

import "fmt"

// EventType 为平台认可的四类扣分事件。
type EventType string

const (
	TypeLateDelivery EventType = "late_delivery" // 超时送达
	TypeComplaint    EventType = "complaint"     // 客诉成立
	TypeRejectOrder  EventType = "reject_order"  // 拒单
	TypeFaultCancel  EventType = "fault_cancel"  // 责任取消
)

// Config 为系统构造参数。
type Config struct {
	PeriodLen       int64             // 周期长度（秒，>0）
	PeriodBase      int64             // 周期起始基准时刻
	Deductions      map[EventType]int // 各事件类型扣分值（>0）
	Thresholds      []int             // 严格递增的定级分数阈值
	AppealWindow    int64             // 申诉窗口时长（>0）
	ClusterSpan     int64             // 根因簇时间跨度（>=0）
	MaxDownPerCycle int               // 每周期最多下降的等级数（>0）
	CompPerLevel    int               // 每级补偿额（>=0）
}

// Event 是一条已登记的扣分事件。
type Event struct {
	ID        string
	Rider     string
	OccurAt   int64
	Type      EventType
	RootCause string // 为空表示无根因标识，独立计扣分
	Revoked   bool
}

// Appeal 是一条申诉记录。
type Appeal struct {
	ID      string
	EventID string
	Rider   string
	FiledAt int64
	Ruled   bool
	Upheld  bool // 裁决是否成立
	RuledAt int64
}

// Compensation 是一笔回溯补偿账目。
type Compensation struct {
	AppealID     string
	Rider        string
	Period       int
	FrozenGrade  int
	DesiredGrade int
	Levels       int
	Amount       int
}

// PeriodReport 是某骑手某周期在某查询时刻的视图。
type PeriodReport struct {
	Period  int
	Settled bool // 该周期在查询时刻是否已结算
	Score   int  // 未撤销计扣分事件的扣分之和（结算后为冻结值）
	Grade   int  // 分数等级：数字越大越差
	Benefit int  // 该周期生效的权益等级
	Frozen  bool
}

func (c Config) periodIndex(t int64) int {
	d := t - c.PeriodBase
	q := d / c.PeriodLen
	if d < 0 && d%c.PeriodLen != 0 {
		q-- // Go 整除向零取整，需改为向下取整
	}
	return int(q)
}

func (c Config) periodEnd(p int) int64 {
	return c.PeriodBase + int64(p+1)*c.PeriodLen
}

// gradeOf 按阈值划分等级，总额恰等于某阈值时落入该阈值对应的更差一级。
func (c Config) gradeOf(score int) int {
	g := 0
	for _, th := range c.Thresholds {
		if score >= th {
			g++
		} else {
			break // 阈值严格递增
		}
	}
	return g
}

// ErrCode 可程序化区分的错误码。
type ErrCode int

const (
	ErrInvalidParam ErrCode = iota + 1
	ErrClockRollback
	ErrRiderNotFound
	ErrEventNotFound
	ErrAppealNotFound
	ErrEventRevoked
	ErrAlreadyAppealed
	ErrAppealRuled
	ErrSatelliteEvent
	ErrAppealWindowExpired
)

// Error 携带可程序化区分的错误码。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(code ErrCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// validate 校验构造参数，任何非法都在系统被使用前拒绝。
func validate(c Config) error {
	if c.PeriodLen <= 0 {
		return errf(ErrInvalidParam, "period length must be positive, got %d", c.PeriodLen)
	}
	if c.AppealWindow <= 0 {
		return errf(ErrInvalidParam, "appeal window must be positive, got %d", c.AppealWindow)
	}
	if c.ClusterSpan < 0 {
		return errf(ErrInvalidParam, "cluster span must be non-negative, got %d", c.ClusterSpan)
	}
	if c.MaxDownPerCycle <= 0 {
		return errf(ErrInvalidParam, "max-down per cycle must be positive, got %d", c.MaxDownPerCycle)
	}
	if c.CompPerLevel < 0 {
		return errf(ErrInvalidParam, "compensation per level must be non-negative, got %d", c.CompPerLevel)
	}
	if len(c.Deductions) == 0 {
		return errf(ErrInvalidParam, "at least one deduction type is required")
	}
	for t, d := range c.Deductions {
		if d <= 0 {
			return errf(ErrInvalidParam, "deduction for %q must be positive, got %d", t, d)
		}
	}
	prev := -1
	for i, th := range c.Thresholds {
		if th <= prev {
			return errf(ErrInvalidParam, "thresholds must be strictly increasing, index %d = %d", i, th)
		}
		prev = th
	}
	return nil
}
