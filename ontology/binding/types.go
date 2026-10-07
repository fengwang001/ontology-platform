package binding

import (
	"fmt"
	"strconv"
)

// TypeKind 标识字段值的基础类型种类。
type TypeKind string

const (
	KindString  TypeKind = "string"
	KindInt     TypeKind = "integer"
	KindDecimal TypeKind = "decimal"
	KindBool    TypeKind = "boolean"
)

// FieldType 是某版本下字段的取值类型声明。
type FieldType struct {
	Kind TypeKind
	// ComparableWith 声明本类型在当前版本下允许与哪些异种类型比较。
	// 缺省时只有同种类类型可比较。
	ComparableWith []TypeKind
}

// comparableTo 报告 t 与 other 在类型系统层面是否可比较。
func (t FieldType) comparableTo(other FieldType) bool {
	if t.Kind == other.Kind {
		return true
	}
	for _, k := range t.ComparableWith {
		if k == other.Kind {
			return true
		}
	}
	for _, k := range other.ComparableWith {
		if k == t.Kind {
			return true
		}
	}
	return false
}

// Value 是字段取值空间中的一个具体取值。Raw 支持的承载类型：
// string、int64、bool；KindDecimal 使用规范化后的 string 承载。
type Value struct {
	Raw any
}

// StringKey 把外部输入的 Raw 统一转换为可比字符串键。
func StringKey(raw any) (string, error) {
	switch x := raw.(type) {
	case string:
		return "s:" + x, nil
	case int64:
		return "i:" + strconv.FormatInt(x, 10), nil
	case int:
		return "i:" + strconv.Itoa(x), nil
	case bool:
		if x {
			return "b:1", nil
		}
		return "b:0", nil
	default:
		return "", fmt.Errorf("binding: unsupported value type %T", raw)
	}
}

// CanonicalKey 返回该取值在其类型域内的规范键。
func (v Value) CanonicalKey() (string, error) {
	return StringKey(v.Raw)
}

// FieldDef 是某一版本下字段的不可变定义。
type FieldDef struct {
	ObjectType string
	Name       string
	Type       FieldType
	// Nullable 为 true 时，缺失取值是取值空间中的一个独立元素。
	Nullable bool
	// Allowed 非空时，字段取值被限制为该枚举集合。
	// 为空时取值域由类型决定（结构域）。
	Allowed []Value
}

// MissingPolicy 决定两个缺失取值之间是否算作彼此对应。
// 该策略在绑定建立时确定，运行期不得改变。
type MissingPolicy struct {
	// MapMissing 为 true 时，左侧缺失 ⇄ 右侧缺失构成确定对应。
	// 为 false 时缺失不参与对应（对端为可缺失字段时构成不兼容）。
	MapMissing bool
}

// ValuePair 是显式声明的一对一取值对应。
type ValuePair struct {
	Left  Value
	Right Value
}

// Correspondence 是绑定建立时声明的确定对应方式。
// Mapping 为空表示“恒等对应”：要求两侧类型可比较且取值域一致。
type Correspondence struct {
	Mapping []ValuePair
	Missing MissingPolicy
}

// Direction 是绑定声明的方向性。
type Direction string

const (
	// Bidirectional 要求对应方式双向唯一（双射）。
	Bidirectional Direction = "bidirectional"
	// LeftToRight 只要求左 → 右 的确定性全函数；反方向查询/写入被拒绝。
	LeftToRight Direction = "left_to_right"
	// RightToLeft 只要求右 → 左 的确定性全函数。
	RightToLeft Direction = "right_to_right"
)

// QueryDirection 描述一次绑定使用的发起方向。
type QueryDirection string

const (
	QLeftToRight QueryDirection = "left_to_right"
	QRightToLeft QueryDirection = "right_to_left"
)

// BindingSpec 是链接类型对绑定依据的完整声明。
type BindingSpec struct {
	LinkType       string
	LeftObject     string
	LeftField      string
	RightObject    string
	RightField     string
	Direction      Direction
	Correspondence Correspondence
}
