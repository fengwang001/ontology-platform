package ontology

// PropertyType 是属性定义中声明的取值类型。
type PropertyType int

const (
	TypeInt PropertyType = iota
	TypeFloat
	TypeString
	TypeBool
)

func (t PropertyType) String() string {
	switch t {
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeBool:
		return "bool"
	}
	return "unknown"
}

// Value 是一个带类型的属性取值。
type Value struct {
	Type PropertyType
	I    int64
	F    float64
	S    string
	B    bool
}

func IntValue(v int64) Value     { return Value{Type: TypeInt, I: v} }
func FloatValue(v float64) Value { return Value{Type: TypeFloat, F: v} }
func StringValue(v string) Value { return Value{Type: TypeString, S: v} }
func BoolValue(v bool) Value     { return Value{Type: TypeBool, B: v} }

// Equal 按类型与取值严格比较。
func (v Value) Equal(o Value) bool {
	if v.Type != o.Type {
		return false
	}
	switch v.Type {
	case TypeInt:
		return v.I == o.I
	case TypeFloat:
		return v.F == o.F
	case TypeString:
		return v.S == o.S
	case TypeBool:
		return v.B == o.B
	}
	return false
}

// CoerceTo 尝试把取值转换到目标类型。
// 允许的转换：同类型恒等；int -> float（放宽）；
// float -> int（收紧，仅当数值为整数且在 int64 范围内）。
// 其余组合均不可转换。
func (v Value) CoerceTo(target PropertyType) (Value, bool) {
	if v.Type == target {
		return v, true
	}
	switch {
	case v.Type == TypeInt && target == TypeFloat:
		return FloatValue(float64(v.I)), true
	case v.Type == TypeFloat && target == TypeInt:
		i := int64(v.F)
		if float64(i) == v.F {
			return IntValue(i), true
		}
		return Value{}, false
	}
	return Value{}, false
}

// PropertyDef 是单个属性的定义。
type PropertyDef struct {
	Name     string
	Type     PropertyType
	Required bool
}

// RecordTime 是记录时间轴（事实写入系统的时刻）。
type RecordTime int64

// ValidTime 是有效时间轴（事实在现实世界成立的时刻）。
type ValidTime int64

// openEnd 表示版本生效区间的开放右端点。
const openEnd RecordTime = RecordTime(1<<62 - 1)

// SchemaVersion 是对象类型的一次属性定义版本。
// 生效区间为左闭右开 [From, To)：记录时刻恰好等于 From 的事实
// 归属本版本；恰好等于 To 的事实归属下一个版本。
// 该边界取等规则对所有版本边界保持单一方向（取右侧/新版本）。
type SchemaVersion struct {
	ID    int64
	Props map[string]PropertyDef
	From  RecordTime // 闭区间下界
	To    RecordTime // 开区间上界，openEnd 表示开放
}

// Contains 判断记录时刻是否落入本版本生效区间 [From, To)。
func (v SchemaVersion) Contains(rt RecordTime) bool {
	return rt >= v.From && rt < v.To
}

// Fact 是一条双时态历史事实：某对象在某有效时间的属性快照，
// 于 RecordTime 被写入系统。同一有效时间允许按记录时间先后
// 存在多条修正轨迹。
type Fact struct {
	ObjectID   string
	ValidTime  ValidTime
	RecordTime RecordTime
	Values     map[string]Value
}

// Migration 描述一次属性定义迁移：以 NewProps 全量替换当前定义，
// 新版本自 EffectiveFrom（含）起生效。
// EffectiveFrom 允许落在过去（追溯迁移），此时被完全遮蔽的旧版本
// 将被作废；记录时刻落在 [EffectiveFrom, +inf) 内的既有事实必须
// 满足新定义约束，否则迁移整体失败。
type Migration struct {
	TypeID        string
	NewProps      map[string]PropertyDef
	EffectiveFrom RecordTime
}

// ExpandRequest 是历史展开请求：在记录时刻 AsOfRecord 可见的范围内，
// 展开对象在有效时间区间 [ValidFrom, ValidTo]（双端闭区间）内的全部
// 历史事实。PinSchemaVersion 可选，要求展开必须依据指定版本；
// 若该版本已被迁移作废，则报 ErrCodeSchemaInvalidated。
type ExpandRequest struct {
	ObjectID         string
	AsOfRecord       RecordTime
	ValidFrom        ValidTime
	ValidTo          ValidTime
	PinSchemaVersion *int64
}
