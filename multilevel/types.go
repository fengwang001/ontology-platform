package multilevel

// Dim 表示一个分组维度的取值：nil 表示空值（真实取值，参与分组），
// 非 nil 表示具体字符串。空值与汇总占位严格区分，汇总占位不出现在 Dim 中。
type Dim = *string

// Level 表示变更/聚合所在的层级号。
const (
	LevelDetail   = 1 // 明细组（第一维 × 第二维）
	LevelSubtotal = 2 // 按第一维的小计
	LevelGrand    = 3 // 总计
)

// Row 是一条输入行：两个分组维度加一个求和度量。
type Row struct {
	Dim1 Dim
	Dim2 Dim
	V    int64
}

// Change 是变更日志中的一条正负变更。
// Count 为计数增量（+1 / -1），Sum 为求和增量，均非零。
type Change struct {
	Seq   int64
	Level int
	Dim1  Dim
	Dim2  Dim // 仅 LevelDetail 使用；小计/总计为 nil（汇总占位）
	Count int64
	Sum   int64
}

// GroupView 是某一层一个组的只读快照。
type GroupView struct {
	Level int
	Dim1  Dim
	Dim2  Dim
	Count int64
	Sum   int64
}

// ReasonCode 是非法输入的可区分原因码。
type ReasonCode string

const (
	ReasonInvalidIncrement ReasonCode = "invalid_increment"
	ReasonRowNotFound      ReasonCode = "row_not_found"
	ReasonDetailGroupLimit ReasonCode = "detail_group_limit"
)
