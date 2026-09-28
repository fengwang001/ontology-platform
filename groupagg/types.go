// Package groupagg 提供支持分组键变更的增量分组聚合组件。
//
// 组件随着行的插入、更新与删除实时维护每个分组的求和（Sum）与计数（Count），
// 并为每次成功的批处理输出一段确定性的变更日志（[]Entry）。下游按顺序应用
// 这些日志条目（先撤回旧值、再写入新值），即可始终得到与当前行表一致的聚合视图；
// 计数为零的分组不会出现在视图中。
package groupagg

import "errors"

// OpKind 表示行级操作的类型。
type OpKind int

const (
	// OpInsert 插入一行。要求行键当前不存在，且分组键非空。
	OpInsert OpKind = iota
	// OpUpdate 更新一行。要求行键已存在，且新分组键非空；分组键与取值均可变更。
	OpUpdate
	// OpDelete 删除一行。要求行键已存在，仅使用 Key 字段。
	OpDelete
)

// String 返回操作类型的可读名称。
func (k OpKind) String() string {
	switch k {
	case OpInsert:
		return "INSERT"
	case OpUpdate:
		return "UPDATE"
	case OpDelete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// Op 是一条行级变更操作。Delete 仅使用 Key；Insert/Update 使用全部字段。
type Op struct {
	Kind  OpKind
	Key   string // 行主键
	Group string // 分组键（Delete 时忽略）
	Value int64  // 参与求和的取值（Delete 时忽略）
}

// String 返回操作的紧凑可读表示，用于日志打印。
func (o Op) String() string {
	switch o.Kind {
	case OpDelete:
		return o.Kind.String() + "{key=" + o.Key + "}"
	default:
		return o.Kind.String() + "{key=" + o.Key + ", group=" + o.Group + ", value=" + itoa(o.Value) + "}"
	}
}

// EntryKind 表示变更日志条目的类型。
type EntryKind int

const (
	// EntryWithdraw 撤回某分组的旧值。下游应从视图中删除该分组。
	EntryWithdraw EntryKind = iota
	// EntryUpsert 写入某分组的新值。下游应将该分组设置为条目携带的 Sum/Count。
	EntryUpsert
)

// String 返回条目类型的可读名称。
func (k EntryKind) String() string {
	switch k {
	case EntryWithdraw:
		return "WITHDRAW"
	case EntryUpsert:
		return "UPSERT"
	default:
		return "UNKNOWN"
	}
}

// Entry 是一条增量变更日志。
//
// 同一分组在一次操作中发生变化时，先输出携带旧 Sum/Count 的 Withdraw，
// 再输出携带新 Sum/Count 的 Upsert；行改分组键时，旧分组的条目整体先于新分组。
// Withdraw 携带撤回前的状态，Upsert 携带写入后的状态（Count 恒大于 0）。
type Entry struct {
	Kind  EntryKind
	Group string
	Sum   int64
	Count int64
}

// String 返回条目的紧凑可读表示，用于日志打印。
func (e Entry) String() string {
	return e.Kind.String() + "{group=" + e.Group + ", sum=" + itoa(e.Sum) + ", count=" + itoa(e.Count) + "}"
}

// GroupState 是一个分组当前的聚合状态。只有 Count > 0 的分组才会出现在视图中。
type GroupState struct {
	Sum   int64
	Count int64
}

// 可区分的拒绝原因，调用方使用 errors.Is 判定。
var (
	// ErrDuplicateRow 插入了行键已存在的行。
	ErrDuplicateRow = errors.New("groupagg: 重复插入已存在的行")
	// ErrRowNotFound 更新或删除了行键不存在的行。
	ErrRowNotFound = errors.New("groupagg: 更新或删除了不存在的行")
	// ErrEmptyGroupKey 插入或更新时分组键为空。
	ErrEmptyGroupKey = errors.New("groupagg: 分组键不能为空")
	// ErrTooManyGroups 操作将使不同分组的数量超过配置的上限。
	ErrTooManyGroups = errors.New("groupagg: 分组数量超过上限")
)

// BatchError 指出批处理中第一条非法操作的位置与原因。
// 通过 Unwrap 暴露上述哨兵错误，可用 errors.Is 判定具体原因。
type BatchError struct {
	OpIndex int // 非法操作在批中的下标（从 0 开始）
	Op      Op  // 非法操作本身
	Err     error
}

// Error 实现 error。
func (e *BatchError) Error() string {
	return "groupagg: 批处理在第 " + itoa(int64(e.OpIndex)) + " 条操作被拒绝（" + e.Op.String() + "）: " + e.Err.Error()
}

// Unwrap 暴露被包装的哨兵错误。
func (e *BatchError) Unwrap() error { return e.Err }
