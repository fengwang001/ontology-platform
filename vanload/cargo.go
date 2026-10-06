// Package vanload 实现厢式货车的配载与卸货顺序校验系统。
package vanload

// Category 货物类别。
type Category uint8

const (
	CategoryGeneral   Category = iota // 普通
	CategoryFood                      // 食品
	CategoryFlammable                 // 易燃
	CategoryOxidizing                 // 氧化
)

// Valid 判断类别是否合法。
func (c Category) Valid() bool { return c <= CategoryOxidizing }

// String 返回类别中文名。
func (c Category) String() string {
	switch c {
	case CategoryGeneral:
		return "普通"
	case CategoryFood:
		return "食品"
	case CategoryFlammable:
		return "易燃"
	case CategoryOxidizing:
		return "氧化"
	default:
		return "非法类别"
	}
}

// Cargo 一件货物：编号、重量（克）、体积（立方厘米）、卸货停靠点序号、类别。
type Cargo struct {
	ID       int
	Weight   int
	Volume   int
	Stop     int
	Category Category
}

// Valid 判断货物参数是否合法（所有量为正整数、类别合法）。
func (c Cargo) Valid() bool {
	return c.ID > 0 && c.Weight > 0 && c.Volume > 0 && c.Stop > 0 && c.Category.Valid()
}

// Compartment 车厢分区配置：载重上限（克）与容积上限（立方厘米）。
// 分区编号由配置顺序决定，从车头到车尾依次为 1,2,...。
type Compartment struct {
	MaxWeight int
	MaxVolume int
}

// RejectKind 操作被拒绝的原因。
type RejectKind int

const (
	KindNone              RejectKind = iota
	KindInvalidArgument              // 参数非法
	KindDuplicateID                  // 编号重复（货物已在车上）
	KindDuplicateInBatch             // 批内编号重复（参数非法）
	KindStopPassed                   // 中途装货时停靠点已过
	KindOrderConflict                // 顺序冲突
	KindIsolationConflict            // 隔离冲突
	KindOverweight                   // 超重
	KindOvervolume                   // 超容
	KindUnloadOrder                  // 卸货顺序错误
)

// severityRank 四类装载约束的严重度：数值越大严重度越低。
// 顺序冲突 > 隔离冲突 > 超重 > 超容。
func severityRank(k RejectKind) int {
	switch k {
	case KindOrderConflict:
		return 0
	case KindIsolationConflict:
		return 1
	case KindOverweight:
		return 2
	case KindOvervolume:
		return 3
	default:
		return -1
	}
}

// String 返回原因中文名。
func (k RejectKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "参数非法"
	case KindDuplicateID:
		return "编号重复"
	case KindDuplicateInBatch:
		return "批内编号重复"
	case KindStopPassed:
		return "停靠点已过"
	case KindOrderConflict:
		return "顺序冲突"
	case KindIsolationConflict:
		return "隔离冲突"
	case KindOverweight:
		return "超重"
	case KindOvervolume:
		return "超容"
	case KindUnloadOrder:
		return "卸货顺序错误"
	default:
		return "无"
	}
}

// Reject 描述一次被拒绝的操作及其判定依据。
type Reject struct {
	// Kind 拒绝原因（批量失败时为归并后的整体原因）。
	Kind RejectKind
	// FailedIndex 批量装货时失败件的下标（0 起）；非批量操作为 -1。
	FailedIndex int
	// CompartmentReasons 四类约束失败时，各分区首个不满足的约束（1 起编号）。
	CompartmentReasons map[int]RejectKind
}

// Error 使 Reject 实现 error 接口。
func (r *Reject) Error() string {
	if r == nil {
		return "<nil>"
	}
	name := r.Kind.String()
	if r.FailedIndex >= 0 {
		return name + "（失败件下标=" + itoa(r.FailedIndex) + "）"
	}
	return name
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
