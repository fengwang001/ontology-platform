package scd

// Op 表示一个生效时间点上的变更操作类型。
type Op uint8

const (
	// OpUnknown 是零值，属于非法操作。
	OpUnknown Op = 0
	// OpUpdate 表示该生效时间点起维度取值更新为指定值。
	OpUpdate Op = 1
	// OpDelete 表示该生效时间点起维度取值被删除，该点不产生历史区间。
	OpDelete Op = 2
)

// MinTime 与 MaxTime 是允许使用的生效时间闭区间边界。
// 域特意收窄于 int64 全范围，使“越界”取值仍可被表达与拒绝。
const (
	MinTime int64 = -1 << 62
	MaxTime int64 = 1<<62 - 1
)

// Event 是一条维度变更事件。生效时间相同的后到事件覆盖先到事件。
type Event struct {
	Key   string
	At    int64
	Op    Op
	Value string
}

// ChangePoint 是某个键在一个生效时间上最终保留下来的变更点。
type ChangePoint struct {
	At    int64
	Op    Op
	Value string
	// Seq 是全局单调的到达序号，用于判定“后到者”与复现到达顺序。
	Seq int64
}

// Interval 是一行历史区间，语义为 [Start, End) 内维度取值为 Value。
type Interval struct {
	Key   string
	Start int64
	End   int64
	Value string
}
