package provenance

// Time 是双时态系统中的时间点，使用整数刻度（epoch 单位由调用方约定）。
// 采用 int64 而非 time.Time，是为了让「非法值」有一个明确、可测试的判定：
// 只有最小整数 IllegalTime 被视为非法，其余值均合法。
type Time int64

// IllegalTime 是唯一被视为非法的时间值。
const IllegalTime Time = -9223372036854775808 // math.MinInt64

// Valid 报告时间点是否为合法值。
func (t Time) Valid() bool { return t != IllegalTime }

// Interval 是左闭右开 [Start, End) 的有效时间区间。
// Start == End 表示空区间（不覆盖任何时间点）；Start > End 为非法区间。
type Interval struct {
	Start Time
	End   Time
}

// Valid 报告区间本身是否合法（不要求非空）。
func (i Interval) Valid() bool {
	return i.Start.Valid() && i.End.Valid() && i.Start <= i.End
}

// Contains 按左闭右开语义报告 t 是否被覆盖：Start <= t < End。
func (i Interval) Contains(t Time) bool {
	return i.Start <= t && t < i.End
}

// ObjectID 标识一个对象实体。同一对象的多次修正共享同一 ID。
type ObjectID string

// LinkID 标识一条链接实体。同一链接的多次修正共享同一 ID。
type LinkID string

// ObjectRecord 是对象实体在某次写入时落定的一条不可变历史记录。
type ObjectRecord struct {
	ID      ObjectID
	WriteAt Time // 该条记录的写入时间
	Valid   Interval
	Seq     int64 // 全局提交序号，仅用于排序/追溯，不参与时间判定
	Exists  bool  // 修正可为「删除/逻辑消亡」：false 表示该写入声明对象不存在（空区间）
}

// LinkRecord 是链接实体在某次写入时落定的一条不可变历史记录。
// 链接两端端点在链接生命周期内不可变；端点可变应建模为删除旧链接、新建新链接。
type LinkRecord struct {
	ID      LinkID
	WriteAt Time
	Valid   Interval
	Seq     int64
	Source  ObjectID
	Target  ObjectID
	Exists  bool
}
