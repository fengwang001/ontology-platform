package ontology

// Config 描述双流区间连接参数。
//
// 设左事件时间为 L、右事件时间为 R，配对条件（两端闭合）为：
//
//	L + LowerBound <= R <= L + UpperBound
//
// 等价于 L ∈ [R-upperBound, R-LowerBound]。
type Config struct {
	// LowerBound 区间下界偏移，R 相对 L 的最小允许差值。
	LowerBound int64
	// UpperBound 区间上界偏移，R 相对 L 的最大允许差值。
	UpperBound int64
	// MaxRetained 一步处理并清理后，两侧允许保留的事件总数上限。
	MaxRetained int
}
