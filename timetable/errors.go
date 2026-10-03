// Package timetable 提供带通告历史的时间依赖有向路网最早到达查询。
//
// 边的通行耗时由分段剖面描述，可随时间通过通告追加或替换；查询可指定
// 历史版本，结果只依赖该版本下可见的边与记录，因而任何之后的操作都
// 不会改变既有版本的查询结果。
package timetable

import "fmt"

// Segment 是耗时剖面中的一个分段：自相对偏移 Offset 起，通行耗时
// Cost；Cost 为 -1 表示该分段封闭（不能在该分段内开始通行）。
// 剖面的第一个分段 Offset 必须为 0，Offset 严格递增，最后一段延伸
// 至无穷远。
type Segment struct {
	Offset int64
	Cost   int64
}

// Leg 是结果路线上的一段：经由边 Edge，于 Dep 时刻出发、Arr 时刻到达。
type Leg struct {
	Edge int
	Dep  int64
	Arr  int64
}

// Result 是一次最早到达查询的结果。
type Result struct {
	// Arrival 是目标节点的最早到达时刻 d(g)。
	Arrival int64
	// Route 是按紧边规则选出的路线，段按经过顺序排列。
	Route []Leg
	// Popped 是本次查询从优先队列中确定（弹出）的节点数，
	// 在目标被确定后立即停止。
	Popped int
}

// ErrorCode 以可区分的原因标识被拒绝的操作与失败的查询。
type ErrorCode int

const (
	// ErrInvalidArgument 参数非法（范围、u==v、剖面非法等）。
	ErrInvalidArgument ErrorCode = iota + 1
	// ErrEdgeLimit 边数已达上限。
	ErrEdgeLimit
	// ErrNoSuchEdge 通告引用了未分配的边编号。
	ErrNoSuchEdge
	// ErrRetroactive 通告生效时刻早于当前时钟 now。
	ErrRetroactive
	// ErrOutOfOrder 通告生效时刻早于该边最后一条记录的生效时刻。
	ErrOutOfOrder
	// ErrRecordLimit 该边已有 64 条有效记录且新通告不会触发替换。
	ErrRecordLimit
	// ErrClockRollback Advance 的目标时刻小于当前 now。
	ErrClockRollback
	// ErrVersionNotYetCreated 查询版本大于当前版本。
	ErrVersionNotYetCreated
	// ErrUnreachable 目标在指定出发时刻不可达。
	ErrUnreachable
)

// Error 携带可区分的错误码与说明。
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

func errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
