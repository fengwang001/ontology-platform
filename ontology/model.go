package ontology

import "time"

// InvisibleReason 是三类互斥的不可见原因。
type InvisibleReason string

const (
	ReasonObjectTombstoned InvisibleReason = "OBJECT_TOMBSTONED"
	ReasonRecordTombstoned InvisibleReason = "RECORD_TOMBSTONED"
	ReasonNoValidRecord    InvisibleReason = "NO_VALID_RECORD_AT_TIME"
)

// HistoryRecord 是某对象某属性的一条不可变历史记录，
// 自 EffectiveAt 起生效，直到被同属性更晚的记录取代。
type HistoryRecord struct {
	ID          string
	ObjectID    string
	Property    string
	Value       string
	EffectiveAt time.Time
}

// Conditions 是判定可见性/操作合法性时核对的三层条件取值。
// nil 表示该层对当前操作不适用。
type Conditions struct {
	ObjectAlive *bool `json:"object_alive"`
	RecordLive  *bool `json:"record_live"`
	IsLatestAt  *bool `json:"is_latest_at"`
}

// VisibilityResult 是可见性查询结果。
type VisibilityResult struct {
	Visible  bool
	Reason   InvisibleReason
	RecordID string
	Value    string
}

// CounterSnapshot 记录 store 内部对“属性历史相关数据结构”的触碰次数，
// 用于可复现地证明对象整体删除/复活是 O(1)。
type CounterSnapshot struct {
	RecordAccesses   int64
	TombstoneTouches int64
}
