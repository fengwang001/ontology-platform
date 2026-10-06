package edgecache

import "errors"

// 错误类别（按固定优先级从高到低排列）：
// 参数非法 > 范围不可满足 > 容量不足 > 回源失败 > 版本震荡。
// 调用方可用 errors.Is 判定类别；范围不可满足的具体原因可用
// errors.As 取出 *RangeError 后读取 Reason 进一步区分。
var (
	ErrInvalidArgument     = errors.New("edgecache: invalid argument")
	ErrRangeNotSatisfiable = errors.New("edgecache: range not satisfiable")
	ErrCapacityExceeded    = errors.New("edgecache: capacity exceeded")
	ErrSourceFailure       = errors.New("edgecache: source failure")
	ErrVersionThrash       = errors.New("edgecache: version thrashing")
)

// RangeReason 区分范围不可满足的具体原因。
type RangeReason int

const (
	// RangeStartBeyondTotal 起点不小于对象总长度。
	RangeStartBeyondTotal RangeReason = iota + 1
	// RangeSuffixZero 后缀长度为零。
	RangeSuffixZero
	// RangeEndBeforeStart 终点小于起点。
	RangeEndBeforeStart
)

func (r RangeReason) String() string {
	switch r {
	case RangeStartBeyondTotal:
		return "start beyond total length"
	case RangeSuffixZero:
		return "zero suffix length"
	case RangeEndBeforeStart:
		return "end before start"
	}
	return "unknown"
}

// RangeError 携带范围不可满足的具体原因，errors.Is(err, ErrRangeNotSatisfiable) 为真。
type RangeError struct {
	Reason RangeReason
	Key    string
	Start  int64
	End    int64
	Total  int64
}

func (e *RangeError) Error() string {
	return ErrRangeNotSatisfiable.Error() + ": " + e.Reason.String()
}

func (e *RangeError) Unwrap() error { return ErrRangeNotSatisfiable }
