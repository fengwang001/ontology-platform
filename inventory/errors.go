package inventory

// ErrKind 拒绝原因分类。声明顺序即统一拒绝次序：
// 参数非法 > 时钟回退 > 条目或航段不存在 > 预占已过期 > 状态不符 > 库存不足。
// 一个操作同时违反多类时，只报序号最小（最靠前）的一类。
type ErrKind int

const (
	ErrInvalidParam ErrKind = iota
	ErrClockRollback
	ErrNotFound
	ErrHoldExpired
	ErrStateConflict
	ErrInsufficientInventory
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "条目或航段不存在"
	case ErrHoldExpired:
		return "预占已过期"
	case ErrStateConflict:
		return "状态不符"
	case ErrInsufficientInventory:
		return "库存不足"
	}
	return "未知错误"
}

// Error 是所有被拒绝操作返回的错误类型，携带足够的定位信息。
type Error struct {
	Kind ErrKind
	// Segment 在航段不存在时报缺失的航段；在库存不足时报行程顺序中
	// 第一个不满足的航段。
	Segment string
	// EntryID 在涉及条目的错误（不存在、已过期、状态不符）中为条目号。
	EntryID uint64
	// State 仅在 ErrStateConflict 时有意义，为条目当前状态，
	// 可分辨是已取消还是已出票。
	State  State
	Detail string
}

func (e *Error) Error() string {
	s := e.Kind.String()
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	if e.Segment != "" {
		s += " (航段 " + e.Segment + ")"
	}
	if e.EntryID != 0 {
		s += " (条目 " + itoa(e.EntryID) + ")"
	}
	if e.Kind == ErrStateConflict {
		s += " (当前状态 " + e.State.String() + ")"
	}
	return s
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func paramErr(detail string) *Error {
	return &Error{Kind: ErrInvalidParam, Detail: detail}
}
