package agg

// Row 表示一批输入中的一行操作。
//
// OpAdd 表示新增一行：把 Amount 计入求和、计数加一，并把 Value 的出现行数加一。
// OpWithdraw 表示撤回一行：把 Amount 从求和中扣除、计数减一，并把 Value 的出现行数减一。
// Amount 为整数，保证任意切批下求和与平均值都可精确复现；平均值只在查询时做一次除法。
type Row struct {
	Op     string // 合法值：agg.OpAdd / agg.OpWithdraw
	Group  string // 组名，不允许为空
	Value  string // 去重维度的取值，允许重复出现
	Amount int64  // 该行贡献的数值
}

const (
	OpAdd      = "add"
	OpWithdraw = "withdraw"
)

// PartialGroup 是单个组在一批内的局部部分聚合结果。
//
// 去重计数不可拆成标量，因此按值下推为「净增减行数」映射：
// ValueDeltas[v] 是本批内值 v 的 add 次数减 withdraw 次数（可能为负）。
// 全局合并后某值行数归零即从该组的值映射中删除。
type PartialGroup struct {
	Group       string
	SumDelta    int64
	CountDelta  int64
	ValueDeltas map[string]int64
}

// Partial 是一批输入在本地阶段产出的部分聚合。
// 全局阶段只接受这一种载荷，不接受原始行。
type Partial struct {
	Groups []PartialGroup
}

// Result 是一个组在全局状态中的查询结果。
//
// Sum、Count 由部分聚合直接相加得到；
// Avg 仅在查询时由 Sum/Count 计算（Count 为 0 的组不会出现在结果中）；
// DistinctCount 为全局值映射中行数大于 0 的不同值个数。
type Result struct {
	Group         string
	Sum           int64
	Count         int64
	Avg           float64
	DistinctCount int
}
