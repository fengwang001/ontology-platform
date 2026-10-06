package van

// Category 货物类别。
type Category int

const (
	General   Category = iota // 普通
	Flammable                 // 易燃
	Oxidizer                  // 氧化
	Food                      // 食品
)

// Cargo 单件货物。所有数值字段均为正整数。
type Cargo struct {
	ID     int      // 货物编号
	Weight int      // 重量，克
	Volume int      // 体积，立方厘米
	Stop   int      // 卸货停靠点序号（1 最先卸）
	Kind   Category // 类别
}

// RejectReason 四类装货失败原因，严重度由高到低排列。
type RejectReason int

const (
	ReasonOrder      RejectReason = iota // 顺序冲突
	ReasonIsolation                      // 隔离冲突
	ReasonOverWeight                     // 超重
	ReasonOverVolume                     // 超容
)

// RejectError 参数校验通过、但装货被四类约束拒绝时返回的错误。
type RejectError struct{ Reason RejectReason }

func (e *RejectError) Error() string {
	switch e.Reason {
	case ReasonOrder:
		return "rejected: order conflict"
	case ReasonIsolation:
		return "rejected: isolation conflict"
	case ReasonOverWeight:
		return "rejected: overweight"
	case ReasonOverVolume:
		return "rejected: over volume"
	default:
		return "rejected"
	}
}

// 固定错误：非法参数 / 编号重复 / 停靠点已过 / 卸货顺序错误 / 货物不存在。
var (
	ErrInvalid    = invalidErr("invalid argument")
	ErrDuplicate  = dupErr("duplicate cargo id")
	ErrStopPassed = stopErr("stop already passed")
	ErrOrderError = orderErr("unload out of order")
	ErrNotFound   = notFoundErr("cargo not found")
	ErrProcessed  = processedErr("stop already processed")
)
