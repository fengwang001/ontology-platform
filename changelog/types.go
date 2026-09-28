// Package changelog 把按批到达的写入流折叠为撤回式变更日志。
//
// 每个键要么不存在，要么持有一个值。一批写入只比较每个被触及键
// 在批开始前与批结束后的净状态：状态相同则不产生日志，否则按
// “先撤回旧值、再写入新值”的顺序输出条目。
package changelog

import "fmt"

// Op 表示批内单条变更的操作类型。
type Op int

const (
	// OpUnknown 是零值占位，属于非法操作。
	OpUnknown Op = 0
	// OpPut 写入一个值，使键存在。
	OpPut Op = 1
	// OpDelete 删除键，使键不存在。
	OpDelete Op = 2
)

// Mutation 是一批写入中的一条变更意图。
type Mutation struct {
	// Key 为目标键，不允许为空字符串。
	Key string
	// Op 为操作类型，只允许 OpPut 或 OpDelete。
	Op Op
	// Value 为 OpPut 时写入的值；OpDelete 时忽略。
	Value string
}

// EntryKind 表示变更日志条目的种类。
type EntryKind int

const (
	// EntryRetract 撤回一个此前存在的旧值。
	EntryRetract EntryKind = 1
	// EntryUpsert 写入一个新值。
	EntryUpsert EntryKind = 2
)

// Entry 是撤回式变更日志中的一条不可变记录。
type Entry struct {
	// Key 为条目所属键。
	Key string
	// Kind 为条目种类（撤回 / 写入）。
	Kind EntryKind
	// Value 为被撤回的旧值或新写入的值。
	Value string
}

// String 返回操作的可读名称。
func (o Op) String() string {
	switch o {
	case OpPut:
		return "PUT"
	case OpDelete:
		return "DELETE"
	default:
		return fmt.Sprintf("INVALID(%d)", int(o))
	}
}

// String 返回条目种类的可读名称。
func (k EntryKind) String() string {
	switch k {
	case EntryRetract:
		return "RETRACT"
	case EntryUpsert:
		return "UPSERT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(k))
	}
}
