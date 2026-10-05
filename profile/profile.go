// Package profile 定义测点参数、运行时状态以及共享词汇（错误码与上报原因）。
package profile

import "errors"

var (
	ErrInvalid   = errors.New("invalid argument")
	ErrClockBack = errors.New("clock moved backward")
	ErrNoPoint   = errors.New("no such point")
)

const (
	MaxT    int64 = 1_000_000_000_000     // 时钟上限（毫秒）
	MaxStep int64 = 10_000_000            // 单次调用最大推进（毫秒）
	MaxAbsV int64 = 1_000_000_000_000_000 // 样本值与量程端点的绝对值上限
	MaxDB   int64 = 1_000_000_000         // 死区上限
	MinMaxI int64 = 1000                  // 心跳间隔下限
	MaxMaxI int64 = 1_000_000_000         // 心跳间隔上限
)

// Reason 为上报原因。
type Reason int

const (
	ReasonFirst Reason = iota
	ReasonChange
	ReasonTrailing
	ReasonHeartbeat
	ReasonRecover
)

func (r Reason) String() string {
	switch r {
	case ReasonFirst:
		return "First"
	case ReasonChange:
		return "Change"
	case ReasonTrailing:
		return "Trailing"
	case ReasonHeartbeat:
		return "Heartbeat"
	case ReasonRecover:
		return "Recover"
	}
	return "Unknown"
}

// Params 为测点参数，热更新时整体替换。
type Params struct {
	DB   int64 // 死区
	MinI int64 // 最小上报间隔
	MaxI int64 // 心跳间隔
	Lo   int64 // 量程下限
	Hi   int64 // 量程上限
}

// Valid 校验参数合法性。
func (p Params) Valid() bool {
	if p.DB < 0 || p.DB > MaxDB {
		return false
	}
	if p.MinI < 1 || p.MinI >= p.MaxI {
		return false
	}
	if p.MaxI < MinMaxI || p.MaxI > MaxMaxI {
		return false
	}
	if p.Lo > p.Hi {
		return false
	}
	return Abs(p.Lo) <= MaxAbsV && Abs(p.Hi) <= MaxAbsV
}

// ValueOK 校验样本值合法（绝对值不超过 10^15）。
func ValueOK(v int64) bool { return Abs(v) <= MaxAbsV }

// Abs 返回绝对值。
func Abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// State 为测点运行时状态；热更新保留这些字段。
type State struct {
	Params
	LastV   int64 // 最近一次上报的值
	HasLast bool  // 是否已有上报
	LastAt  int64 // 最近一次上报的时刻
	CurTS   int64 // 最近有效样本时刻
	CurV    int64 // 最近有效样本值
	HasCur  bool  // 是否有有效样本
	Bad     int64 // 连续无效样本数
	Fault   bool  // 故障标志
	Defer   int64 // 推迟下限
}
