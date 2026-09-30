// Package cdc 提供主键变更的拆分与分区投递能力：
// 将源表的一批变更（插入/更新/删除）拆分为下游可识别的
// 写入/删除事件，合并后按主键哈希分区投递，保证下游视图
// 与源表始终一致。
package cdc

// Row 是源表中的一行数据，主键本身不作为行内字段，
// 而是由 Change.Key / Event.Key 单独承载。
type Row map[string]string

// ChangeKind 标识源表变更类型。
type ChangeKind int

const (
	// Insert 插入一行，Key 为新主键。
	Insert ChangeKind = iota
	// Update 更新一行；OldKey 非空且不等于 Key 时表示主键发生变更。
	Update
	// Delete 删除一行。
	Delete
)

// String 返回变更类型的可读名称，用于日志输出。
func (k ChangeKind) String() string {
	switch k {
	case Insert:
		return "INSERT"
	case Update:
		return "UPDATE"
	case Delete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// Change 描述对源表的一条变更。
type Change struct {
	Kind   ChangeKind
	Key    string // 目标主键（Insert/Update/Delete 均使用）
	OldKey string // 仅 Update 且主键变更时填写旧主键；为空或等于 Key 表示主键不变
	Row    Row    // Insert/Update 携带的行数据；Delete 忽略
}

// Op 是投递给下游的事件操作类型。
type Op int

const (
	// OpWrite 写入（覆盖）一个主键对应的行。
	OpWrite Op = iota
	// OpDelete 删除一个主键对应的行。
	OpDelete
)

// String 返回事件操作的可读名称，用于日志输出。
func (o Op) String() string {
	switch o {
	case OpWrite:
		return "WRITE"
	case OpDelete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// Event 是拆分后投递给下游的事件。
type Event struct {
	Seq int // 在本批拆分序列中的位置（从 0 开始），保证分区内有序
	Op  Op
	Key string
	Row Row // 仅 OpWrite 有效
}
