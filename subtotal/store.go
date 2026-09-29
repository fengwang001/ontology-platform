package subtotal

// Row 是参与分组的明细行。Dim1、Dim2 为两个分组维度，允许为 nil，
// nil 表示真实的空值取值，与汇总层使用的“全部”占位严格区分。
type Row struct {
	ID     string
	Dim1   *string
	Dim2   *string
	Amount int64
}

// Layer 标识一条变更所属的层级。
const (
	LayerDetail = 1 // 明细组：(Dim1, Dim2)
	LayerSub1   = 2 // 第一维小计：Dim1
	LayerTotal  = 3 // 总计：全部
)

// Change 是一条三层变更日志。Delta 对 upsert 为正、对撤回为负；
// 当组此前不存在时 Created 为 true，计数归零被删除时 Removed 为 true。
// Dim1/Dim2 为 nil 表示该维度的真实空值分组；Grand 为 true 表示总计占位。
type Change struct {
	Seq     int64
	Op      string
	Layer   int
	Dim1    *string
	Dim2    *string
	Grand   bool
	Count   int64
	Sum     int64
	Delta   int64
	CDelta  int64
	Created bool
	Removed bool
}

// GroupStat 是某一层某个组的当前计数与求和。
type GroupStat struct {
	Count int64
	Sum   int64
}

// Key1 是第一维小计组的键。Null=true 表示该维度的真实空值取值，
// 与“全部/总计”的汇总占位严格区分（后者不进入此映射，用 Grand 标记）。
type Key1 struct {
	V    string
	Null bool
}

// Key2 是明细组的键；ANull/BNull 为真表示对应维度取真实空值。
type Key2 struct {
	A     string
	ANull bool
	B     string
	BNull bool
}

// Snapshot 是某一时刻三层状态的一致视图。
type Snapshot struct {
	Details map[Key2]GroupStat
	Sub1s   map[Key1]GroupStat
	Total   GroupStat
}

// RejectCode 是非法输入的可区分错误类别。
type RejectCode string

const (
	RejectMalformed     RejectCode = "MALFORMED_INCREMENT" // 增量结构非法（未知 op、空 ID）
	RejectRowIDNotFound RejectCode = "ROW_ID_NOT_FOUND"    // 撤回当前不存在的行
	RejectDetailLimit   RejectCode = "DETAIL_GROUP_LIMIT"  // 新明细组数将超过上限
	RejectAmountInvalid RejectCode = "AMOUNT_INVALID"      // 求和将发生 int64 溢出
)

// RejectError 携带互不相同、可区分的拒绝原因；被整体拒绝时状态不变。
type RejectError struct {
	Code   RejectCode
	Reason string
}

func (e *RejectError) Error() string { return string(e.Code) + ": " + e.Reason }
