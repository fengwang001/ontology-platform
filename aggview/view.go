package aggview

import (
	"errors"
	"io"
)

// 过滤条件相关的固定阈值在 Config 中给出。

var (
	// ErrEmptyRowID 表示行 ID 为空。
	ErrEmptyRowID = errors.New("aggview: empty row id")
	// ErrEmptyGroupName 表示组名为空。
	ErrEmptyGroupName = errors.New("aggview: empty group name")
	// ErrRowNotFound 表示删除的行当前不存在。
	ErrRowNotFound = errors.New("aggview: row not found")
	// ErrDuplicateRowID 表示同一批次或已有数据中出现重复行 ID。
	ErrDuplicateRowID = errors.New("aggview: duplicate row id")
	// ErrUnknownOp 表示变更操作类型未知。
	ErrUnknownOp = errors.New("aggview: unknown operation")
	// ErrTooManyGroups 表示变更后不同组的数量超过 MaxGroups。
	ErrTooManyGroups = errors.New("aggview: group count exceeds limit")
	// ErrInvalidConfig 表示 Config 中的阈值配置非法。
	ErrInvalidConfig = errors.New("aggview: invalid config")
)

// Op 标识一条行级变更的类型。
type Op int

const (
	// OpInsert 插入一行。
	OpInsert Op = iota + 1
	// OpDelete 删除一行。
	OpDelete
)

// Change 是一条行级变更：插入或删除一行 (RowID, Group, Value)。
type Change struct {
	Op     Op
	RowID  string
	Group  string
	Value  int64
}

// Config 配置分组聚合视图的过滤条件。
// 一个组出现在视图中，当且仅当：
// 行数 >= MinCount 且 求和 >= MinSum（均含等于）。
type Config struct {
	MinCount  int64
	MinSum    int64
	MaxGroups int
}

// Kind 标识一条视图输出条目的类型。
type Kind int

const (
	// KindEnter 表示组进入视图：只写新值。
	KindEnter Kind = iota + 1
	// KindLeave 表示组离开视图：只撤回旧值。
	KindLeave
	// KindUpdate 表示组在视图内发生变化：先撤回旧值再写新值。
KindUpdate
)

// Entry 是一条面向下游的净变化输出。
// Kind 为 Enter 时仅 New 有效；Leave 时仅 Old 有效；
// Update 时 Old/New 均有效，下游必须先应用 Retract(Old) 再 Put(New)。
type Entry struct {
	Kind  Kind
	Group string
	Old   Aggregate
	New   Aggregate
}

// Aggregate 是一个组的聚合结果。
type Aggregate struct {
	Count int64
	Sum   int64
}

// View 是带过滤条件的分组聚合视图，支持行级插入/删除的增量维护。
type View struct {
}

// New 创建一个空视图。
func New(cfg Config, logWriter io.Writer) *View {
	return nil
}

// Apply 原子地应用一批变更，返回视图的净变化输出。
// 批中任何一条非法都会导致整批被拒绝，状态与输出保持不变。
func (v *View) Apply(changes []Change) ([]Entry, error) {
	return nil, nil
}

// Snapshot 返回当前视图中满足过滤条件的所有组的快照副本。
func (v *View) Snapshot() map[string]Aggregate {
	return nil
}

// Get 返回某个组当前在视图中的聚合值。
func (v *View) Get(group string) (Aggregate, bool) {
	return Aggregate{}, false
}

// GroupCount 返回当前存在（行数大于零）的不同组的数量。
func (v *View) GroupCount() int {
	return 0
}
