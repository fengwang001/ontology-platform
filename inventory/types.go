// Package inventory 实现电商多仓库存的可承诺量（ATP）查询与订单承诺。
//
// 系统在现货、计划入库、带到期时刻的预留与仓库优先序的共同作用下，
// 为订单行选择发货仓，必要时按仓库优先序拆分，并区分永久缺货与暂时缺货。
// 所有操作可并发调用，内部以单互斥锁串行化，结果等价于某个串行顺序。
package inventory

// Reason 表示操作被拒绝的原因类别。
// 常量的声明顺序即拒绝优先级（数值小者优先被报告）。
type Reason int

const (
	// ReasonInvalidParam 参数非法（数量为负、订单号为空、查询时刻早于当前时钟等）。
	ReasonInvalidParam Reason = iota
	// ReasonClockRollback 时钟回退：操作携带的时刻小于上一次被接受操作的时刻。
	ReasonClockRollback
	// ReasonDuplicateOrder 订单重复：同一订单号已存在仍有效的预留。
	ReasonDuplicateOrder
	// ReasonPermanentShortage 永久缺货：所有仓库现货加全部计划入库总和即不足。
	ReasonPermanentShortage
	// ReasonTemporaryShortage 暂时缺货：总和足够，但受预留或到货时刻限制不足。
	ReasonTemporaryShortage
	// ReasonTooManySplits 拆分过多：整张订单涉及的不同仓库数超过上限。
	ReasonTooManySplits
	// ReasonReservationExpired 预留已过期：确认出库时预留已到期。
	ReasonReservationExpired
	// ReasonOrderNotFound 订单不存在：无此订单的预留，或预留已到期（释放场景）。
	ReasonOrderNotFound
	// ReasonInboundNotFound 计划入库单不存在（或已确认到货，不得重复确认）。
	ReasonInboundNotFound
	// ReasonInsufficientOnHand 确认出库时现货不足（预留由未来到货支撑，尚未转为现货）。
	ReasonInsufficientOnHand
)

// String 返回原因的可读名称，用于日志打印判定依据。
func (r Reason) String() string {
	switch r {
	case ReasonInvalidParam:
		return "参数非法"
	case ReasonClockRollback:
		return "时钟回退"
	case ReasonDuplicateOrder:
		return "订单重复"
	case ReasonPermanentShortage:
		return "永久缺货"
	case ReasonTemporaryShortage:
		return "暂时缺货"
	case ReasonTooManySplits:
		return "拆分过多"
	case ReasonReservationExpired:
		return "预留已过期"
	case ReasonOrderNotFound:
		return "订单不存在"
	case ReasonInboundNotFound:
		return "入库单不存在"
	case ReasonInsufficientOnHand:
		return "现货不足"
	default:
		return "未知原因"
	}
}

// Error 描述一次被拒绝的操作。被拒绝的操作不改变任何状态与时钟。
type Error struct {
	Reason Reason
	// Line 仅对缺货类错误有意义，为缺货订单行的下标（从 0 开始）；其他情况为 -1。
	Line   int
	Detail string
}

func (e *Error) Error() string {
	if e.Line >= 0 {
		return e.Reason.String() + "（订单行 " + itoa(e.Line) + "）：" + e.Detail
	}
	return e.Reason.String() + "：" + e.Detail
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func newError(reason Reason, line int, detail string) *Error {
	return &Error{Reason: reason, Line: line, Detail: detail}
}

// OrderLine 是订单承诺中的一行：商品与数量。
type OrderLine struct {
	Product string
	Qty     int64
}

// Allocation 是一条发货分配：某订单行从某仓库取出的数量。
type Allocation struct {
	LineIndex int
	Warehouse string
	Product   string
	Qty       int64
}

// CommitResult 是订单承诺成功后的结果。
type CommitResult struct {
	OrderID string
	// ExpireAt 为预留到期时刻（承诺时刻 + 预留时长）。
	ExpireAt int64
	// Allocations 按订单行次序、行内按仓库优先序排列。
	Allocations []Allocation
}

// ReservationDetail 是某订单一条预留的明细（查询用）。
type ReservationDetail struct {
	Warehouse string
	Product   string
	Qty       int64
	ExpireAt  int64
	// Valid 表示在当前时钟下该预留是否仍有效（当前时刻严格小于到期时刻）。
	Valid bool
}

// Stats 是运行统计，用于以可验证的方式证明预留考察次数的界。
type Stats struct {
	// ReservationsExamined 是系统至今考察（读取）过的预留记录条数。
	// 每条失效预留至多被考察一次即被物理移除，因此该计数不随
	// 历史订单总数或已失效预留数增长（摊还意义下为常数）。
	ReservationsExamined int64
}

// metrics 内部计数器，由 Stats 快照导出。
type metrics struct {
	reservationsExamined int64
}
