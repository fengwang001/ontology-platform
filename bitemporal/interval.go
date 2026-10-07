package bitemporal

import "fmt"

// Axis 标识一条时间轴。
type Axis string

const (
	ValidTime       Axis = "valid_time"
	TransactionTime Axis = "transaction_time"
)

// BoundMode 描述区间端点的闭合方式。
type BoundMode string

const (
	Closed   BoundMode = "closed"    // [start, end]
	HalfOpen BoundMode = "half_open" // [start, end)
)

// Interval 是一条时间轴上的取值区间。
type Interval struct {
	Start      int64
	End        int64
	StartBound BoundMode
	EndBound   BoundMode
}

// MinFinite / MaxFinite 是允许的业务时间戳（epoch 整数）取值域。
const (
	MinFinite int64 = -1 << 62
	MaxFinite int64 = 1<<62 - 1
)

// ErrIntervalInconsistent 表示记录自身时态区间不自洽。
// 这是判定优先级中最高的一类错误：记录自身的问题先于任何
// 格式版本兼容性问题报告，且二者必须区分。
type ErrIntervalInconsistent struct {
	Axis Axis
	What string
}

func (e *ErrIntervalInconsistent) Error() string {
	return fmt.Sprintf("bitemporal: %s interval inconsistent: %s", e.Axis, e.What)
}

// ValidateInterval 校验单个区间是否自洽。
//
// 自洽的定义（与边界约定无关的硬错误）：
//   - 边界模式必须是可识别的 Closed / HalfOpen；
//   - 起止点必须落在允许的业务时间戳取值域 [MinFinite, MaxFinite] 内；
//   - 起点不得晚于终点。起点晚于终点（start > end）是题面明确点名的
//     不自洽情形；起点等于终点时，闭区间合法、半开区间表示空集，
//     空集本身不算不自洽，而在边界转换阶段处理。
func ValidateInterval(axis Axis, iv Interval) error {
	if iv.StartBound != Closed && iv.StartBound != HalfOpen {
		return &ErrIntervalInconsistent{Axis: axis, What: fmt.Sprintf("unrecognized start bound mode %q", iv.StartBound)}
	}
	if iv.EndBound != Closed && iv.EndBound != HalfOpen {
		return &ErrIntervalInconsistent{Axis: axis, What: fmt.Sprintf("unrecognized end bound mode %q", iv.EndBound)}
	}
	if iv.Start < MinFinite || iv.Start > MaxFinite {
		return &ErrIntervalInconsistent{Axis: axis, What: fmt.Sprintf("start %d out of finite domain", iv.Start)}
	}
	if iv.End < MinFinite || iv.End > MaxFinite {
		return &ErrIntervalInconsistent{Axis: axis, What: fmt.Sprintf("end %d out of finite domain", iv.End)}
	}
	if iv.Start > iv.End {
		return &ErrIntervalInconsistent{
			Axis: axis,
			What: fmt.Sprintf("start %d is later than end %d", iv.Start, iv.End),
		}
	}
	return nil
}

// Contains 判断时点 t 是否落在区间内。
func (iv Interval) Contains(t int64) bool {
	if iv.Empty() {
		return false
	}
	if t < iv.Start {
		return false
	}
	if iv.EndBound == Closed {
		return t <= iv.End
	}
	return t < iv.End
}

// Empty 报告区间是否为空集。
func (iv Interval) Empty() bool {
	// 离散整数时点域上，所有区间起点均包含（start 起算），
	// 因此空集 ⇔ 右端半开且 start == end。
	return iv.Start == iv.End && iv.EndBound == HalfOpen
}

// firstPoint 返回区间包含的最小整数时点；空区间返回 0, false。
func (iv Interval) firstPoint() (int64, bool) {
	if iv.Empty() {
		return 0, false
	}
	return iv.Start, true
}

// lastPoint 返回区间包含的最大整数时点；空区间返回 0, false。
func (iv Interval) lastPoint() (int64, bool) {
	if iv.Empty() {
		return 0, false
	}
	if iv.EndBound == Closed {
		return iv.End, true
	}
	return iv.End - 1, true
}
