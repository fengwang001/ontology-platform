package ontology

// Direction 表示绑定声明的方向性。
type Direction int

const (
	// LeftToRight 仅要求左侧字段取值能唯一对应到右侧。
	LeftToRight Direction = iota
	// RightToLeft 仅要求右侧字段取值能唯一对应到左侧。
	RightToLeft
	// TwoWay 要求双向都唯一可对应（双射）。
	TwoWay
)

// Allows 判断声明方向是否允许以 queryDir 方向发起绑定查询/写入。
func (d Direction) Allows(queryDir Direction) bool {
	switch d {
	case TwoWay:
		return queryDir == LeftToRight || queryDir == RightToLeft
	default:
		return d == queryDir
	}
}

// FieldRef 指向某个对象类型下的一个字段。
type FieldRef struct {
	ObjectType string
	FieldID    string
}

// ValuePair 是显式对应关系中的一对取值对应。
type ValuePair struct {
	Left  Value
	Right Value
}

// CorrespondenceKind 对应方式的种类。
type CorrespondenceKind int

const (
	// Identity 同类型取值按相等即对应。
	Identity CorrespondenceKind = iota
	// Explicit 显式枚举取值对。
	Explicit
)

// Correspondence 声明两侧取值之间的对应方式，建立绑定后不可变。
type Correspondence struct {
	Kind  CorrespondenceKind
	Pairs []ValuePair // Kind 为 Explicit 时有效
}

// MissingPolicyKind 缺失取值参与对应关系的方式。
type MissingPolicyKind int

const (
	// MissingForbidden 字段不得允许缺失；若字段后来变为可缺失，缺失取值无对应，唯一性被破坏。
	MissingForbidden MissingPolicyKind = iota
	// MissingToMissing 左侧缺失与右侧缺失彼此对应。
	MissingToMissing
	// MissingToValue 每侧缺失对应到对侧声明的具体取值。
	MissingToValue
)

// MissingPolicy 声明缺失取值如何参与对应，绑定建立时确定，运行期不可变。
type MissingPolicy struct {
	Kind MissingPolicyKind
	// MissingToValue 时有效：左/右侧缺失分别对应到对侧的哪个具体取值。
	LeftMissingTo  Value
	RightMissingTo Value
}

// BindingDecl 是链接类型上关于绑定依据字段的声明。
type BindingDecl struct {
	LinkTypeID     string
	Left           FieldRef
	Right          FieldRef
	Direction      Direction
	Correspondence Correspondence
	Missing        MissingPolicy
}
