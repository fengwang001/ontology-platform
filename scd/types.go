package scd

// EventKind 描述一个维度变更事件的类型。
type EventKind uint8

const (
	// KindUpdate 表示该生效时间起键取 Value。
	KindUpdate EventKind = 1
	// KindDelete 表示该生效时间起键被删除（墓碑）。
	KindDelete EventKind = 2
)

// Event 是一条维度变更事件。
//
// Seq 为变更点的决胜序号：同一键、同一生效时间 At 的多个事件中
// Seq 最大者为准，结果只依赖最终的变更点集合，与提交到达顺序无关。
type Event struct {
	Key   string
	At    int64
	Kind  EventKind
	Value string
	Seq   int64
}

// ChangePoint 是某个键在生效时间 At 上的一个变更点。
type ChangePoint struct {
	At    int64
	Seq   int64
	Kind  EventKind
	Value string
}

// Interval 是历史中的一个左闭右开区间 [Start, End)。
// End == MaxTime 表示右开区间延伸到时间域尽头。
type Interval struct {
	Start int64
	End   int64
	Value string
}

// Contains 报告时间点 at 是否落在区间内（左闭右开）。
func (iv Interval) Contains(at int64) bool {
	return at >= iv.Start && at < iv.End
}
