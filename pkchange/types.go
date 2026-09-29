package pkchange

// Op 是源表变更的种类。
type Op int

const (
	OpInsert Op = iota + 1
	OpUpdate
	OpDelete
)

// EventKind 是拆分后下游可识别的事件种类。
type EventKind int

const (
	EventWrite EventKind = iota + 1
	EventDelete
)

// Row 是源表中的一行数据。
type Row map[string]string

// Change 是一批变更中的单条源表变更。
type Change struct {
	Op     Op
	Key    string // 插入/删除使用；更新时为旧主键
	NewKey string // 仅更新使用，表示新主键
	Data   Row    // 插入或更新写入的数据
}

// Event 是拆分、合并后投递给下游视图的事件。
type Event struct {
	Kind EventKind
	Key  string
	Data Row
}

// Partition 是一个主键哈希分区内的有序事件。
type Partition struct {
	Index  int
	Events []Event
}

func (o Op) String() string        { return opName(o) }
func (k EventKind) String() string { return eventName(k) }
