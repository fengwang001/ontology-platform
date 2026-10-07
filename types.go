package billing

// Reading 是一次读数记录。estimated=true 表示估抄。
type Reading struct {
	Time      int64
	Value     int64
	Estimated bool
	// segStart 标记该读数为某一段（换表后新表 / 初始段）的起始点。
	segStart bool
	// cap 为该读数所属物理表的量程上限（值范围 [0, cap]）。
	cap int64
	// pos 为累计绝对位置：线性插值与翻转判定全部基于 pos。
	pos int64
}

// Meter 是一只逻辑表（换表后仍是同一只表，内部按段区分物理表）。
type Meter struct {
	id       string
	cap      int64 // 当前物理表量程
	readings []*Reading
}

// Occupancy 是一段左闭右开的在住记录 [Start, End)。
type Occupancy struct {
	Start int64
	End   int64
}

// Household 是一户分户。
type Household struct {
	id        string
	area      int64
	meter     *Meter
	occupancy []Occupancy // 始终按 Start 升序、互不重叠
}

// Bill 是一个已结算账期的不可变账单（更正不改本结构）。
type Bill struct {
	Period [2]int64
	// 生成时快照：各户自用、在住权重（比例分子/分母）、面积。
	self   []int64
	weight []occupancyWeight
	area   []int64
	ids    []string
	share  []int64 // 结算当时各户公摊份额（frozen）
	master int64
	shared int64
}

type occupancyWeight struct {
	num int64 // 在住时长（与时长约去公因数后的分子）
	den int64 // 账期时长
}

// Correction 是对单个已结算账期的一次更正。
type Correction struct {
	Period [2]int64
	Reason string
	// 每户应付差额（自用 + 公摊份额的重算前后之差）。
	Deltas map[string]int64
	// SharedDelta 是该账期公摊重算前后之差，恒等于公摊份额差额之和。
	SharedDelta int64
}

// Settlement 是一次结算的结果。
type Settlement struct {
	Period [2]int64
	Self   map[string]int64
	Shared map[string]int64
	Master int64
}
