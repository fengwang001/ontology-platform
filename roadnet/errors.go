package roadnet

import "fmt"

// ErrCode 区分操作被拒绝或查询失败的原因。
type ErrCode int

const (
	// ErrInvalidParam 参数非法（构造参数、节点、时刻、版本或剖面越界，u 等于 v 等）。
	ErrInvalidParam ErrCode = iota
	// ErrEdgeLimit 边数已满。
	ErrEdgeLimit
	// ErrEdgeNotFound Announce 的边编号未分配。
	ErrEdgeNotFound
	// ErrRetroactive Announce 的 eff 早于 now，试图改写过去。
	ErrRetroactive
	// ErrRecordOrder Announce 的 eff 小于该边最后一条记录的 eff。
	ErrRecordOrder
	// ErrRecordLimit 该边已有 64 条生效记录且 eff 大于最后一条的 eff。
	ErrRecordLimit
	// ErrClockRewind Advance 的 t 早于当前 now。
	ErrClockRewind
	// ErrUnreachable 从 s 出发无法到达 g。
	ErrUnreachable
	// ErrVersionNotYet 查询的 ver 大于当前版本，尚未产生。
	ErrVersionNotYet
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrEdgeLimit:
		return "边数已满"
	case ErrEdgeNotFound:
		return "边不存在"
	case ErrRetroactive:
		return "追溯过去"
	case ErrRecordOrder:
		return "记录乱序"
	case ErrRecordLimit:
		return "记录已满"
	case ErrClockRewind:
		return "时钟回退"
	case ErrUnreachable:
		return "不可达"
	case ErrVersionNotYet:
		return "版本尚未产生"
	}
	return fmt.Sprintf("未知错误(%d)", int(c))
}

// OpError 是一次被拒绝的操作或失败的查询，Code 给出可区分的原因。
type OpError struct {
	Op   string
	Code ErrCode
	Msg  string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Code, e.Msg)
}
