package rollup

// Op 为增量操作类型。
type Op int

const (
	// OpUnknown 非法操作占位。
	OpUnknown Op = iota
	// OpAdd 新增一行。
	OpAdd
	// OpRemove 撤回（删除）一条当前存在的行。
	OpRemove
)

func (o Op) String() string {
	switch o {
	case OpAdd:
		return "add"
	case OpRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Row 为一条输入行。Dim1/Dim2 为两个分组维度，指针为 nil 表示空值，
// 空值是真实的分组取值，与“汇总占位”严格区分。
type Row struct {
	ID    string
	Dim1  *string
	Dim2  *string
	Value int64
}

// Increment 为一条增量：新增一行或撤回一条已存在的行。
type Increment struct {
	Op  Op
	Row Row
}

// Layer 标识三层分组的层级号。
type Layer int

const (
	// LayerDetail 明细组：(Dim1, Dim2)。
	LayerDetail Layer = 1
	// LayerSubtotal 第一维小计：Dim1。
	LayerSubtotal Layer = 2
	// LayerGrand 总计：全部行。
	LayerGrand Layer = 3
)

func (l Layer) String() string {
	switch l {
	case LayerDetail:
		return "detail"
	case LayerSubtotal:
		return "subtotal"
	case LayerGrand:
		return "grand"
	default:
		return "invalid"
	}
}

// GroupKey 唯一标识一条变更所作用的组。
// Layer=Detail 时 Dim1/Dim2 均有意义（HasDim* 区分空值）；
// Layer=Subtotal 时仅 Dim1 有意义；
// Layer=Grand 时 HasDim1/HasDim2 均为 false，表示汇总占位，
// 与某一维恰好为空值的明细/小计组严格不同。
type GroupKey struct {
	Layer   Layer
	Dim1    string
	HasDim1 bool
	Dim2    string
	HasDim2 bool
}

// Change 为某一层上的一条带符号变更。计数归零的组只输出撤回
// （CountDelta 为负、计数变为 0），随后该组即被删除。
type Change struct {
	Layer      Layer
	Key        GroupKey
	CountDelta int64
	SumDelta   int64
	// ResultCount/ResultSum 为应用该变更后该组的计数与求和。
	ResultCount int64
	ResultSum   int64
}

// LogEntry 为一条增量按“明细→小计→总计”顺序产生的完整变更记录。
// 只有在增量整体校验通过后才会写入并应用，因此每个 entry 都是
// 原子可见的，任意 entry 前缀重放后三层状态都自洽。
type LogEntry struct {
	Seq     int64
	Op      Op
	Row     Row
	Changes [3]Change
}

// GroupView 为查询结果中的一个组。
type GroupView struct {
	Key   GroupKey
	Count int64
	Sum   int64
}

// Stats 为总计行。
type Stats struct {
	Count int64
	Sum   int64
}

func keyDim(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	return *v, true
}
