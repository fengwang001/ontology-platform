package antijoin

import "sync"

// Op 表示对左/右侧行集合的一次变更操作。
type Op int

const (
	InsertLeft Op = iota
	DeleteLeft
	InsertRight
	DeleteRight
)

// Key 表示连接键；Valid 为 false 时即 SQL 语义中的 NULL。
type Key struct {
	Value any
	Valid bool
}

// Row 是带唯一标识与可选连接键的一行。
type Row struct {
	ID  string
	Key Key
}

// Change 是输入变更流中的一条变更。
type Change struct {
	Op  Op
	Row Row
}

// EventKind 表示结果成员资格变化方向。
type EventKind int

const (
	Enter EventKind = iota + 1
	Leave
)

// LogEntry 是结果变更日志中的一条记录。
type LogEntry struct {
	Seq    int64
	Event  EventKind
	Row    Row
	Reason string
}

// Options 控制视图容量上限。
type Options struct {
	MaxRows int
}

// View 是反连接增量物化视图。
type View struct {
	mu sync.RWMutex
}

// New 创建一个空视图。
func New(opts Options) *View { return nil }

// Apply 原子地应用一批（前缀）输入变更，返回本批产生的有序变更日志。
func (v *View) Apply(changes []Change) ([]LogEntry, error) { return nil, nil }

// Snapshot 返回当前物化结果，按标识排序。
func (v *View) Recompute() []Row { return nil }

// Recompute 从两侧原始行集合批量重算结果，按标识排序。
func (v *View) Recompute() []Row { return nil }

// SelfCheck 校验内部不变量，返回首个发现的问题。
func (v *View) SelfCheck() error { return nil }

// Log 返回已提交变更日志的完整副本。
func (v *View) Log() []LogEntry { return nil }
