package ontology

// Value 是实例属性值。实例存储为有类型的值，聚合只接受数值型属性，
// 传入与声明类型不符的值归一化为「参数非法」。
type Value struct {
	Num  float64 // Int 与 Double 共用的数值表示
	Str  string
	Type ValueType
}

// ValueType 为属性的声明类型。
type ValueType string

const (
	TypeInt    ValueType = "int"
	TypeDouble ValueType = "double"
	TypeString ValueType = "string"
)

// ObjectTypeSchema 声明一个对象类型：主键字段、若干属性字段。
type ObjectTypeSchema struct {
	Name     string
	KeyField string
	Attrs    map[string]ValueType
}

// AggregateDef 声明一个聚合视图：定义在单一源对象类型之上，按 GroupBy
// 属性分组，对 ValueField（数值型）做 SUM。一个对象类型上可挂多个视图
// （不同 GroupBy），一次提交对全部视图的更新是同一个原子事件。
type AggregateDef struct {
	Name       string
	SourceType string
	GroupBy    string
	ValueField string
}

// Record 是一个对象实例的当前可见版本。
type Record struct {
	Key      string
	Version  int64 // 当前版本号；首次成功提交后为 1，拒绝不占用版本号
	Deleted  bool  // 墓碑标记
	Attrs    map[string]Value
	CommitSN int64 // 生效序号（全局提交串行序），即本版本的生效时刻
}

// Write 是一次乐观并发写入（也可用于删除已存在实例，语义是 U）。
type Write struct {
	Type  string
	Key   string
	Prev  int64 // 并发凭证：前序版本号（首次写入必须为 0）
	Attrs map[string]Value
}

// Delete 是一次乐观并发删除。
type Delete struct {
	Type string
	Key  string
	Prev int64 // 并发凭证：前序版本号
}

// CommitResult 是一次提交（写入/删除）成功后的回执。
type CommitResult struct {
	Key      string
	Version  int64 // 提交后的新版本号
	Deleted  bool  // 是否为一次删除提交
	CommitSN int64 // 本次提交的全局生效序号
}
