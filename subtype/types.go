package subtype

import "strings"

// Kind 枚举类型表达式的各种形态。
type Kind int

const (
	KindInvalid Kind = iota
	KindInt
	KindFloat
	KindString
	KindBool
	KindTop
	KindBottom
	KindObject
	KindFunc
	KindUnion
	KindRef
)

// Prop 是对象类型的一个属性。
type Prop struct {
	Name     string
	Type     *Type
	Optional bool
	ReadOnly bool
}

// Type 是一个类型表达式。构造后应视为不可变：
// 登记到 Registry 或被用于判定之后不得再修改其字段。
//
// 各 Kind 使用的字段：
//   - KindObject: Props
//   - KindFunc:   Params, Ret
//   - KindUnion:  Members（经 Union 构造后保证非空、无嵌套联合、无底类型成员）
//   - KindRef:    Name
//   - 其余:       无
type Type struct {
	Kind    Kind
	Props   []Prop
	Params  []*Type
	Ret     *Type
	Members []*Type
	Name    string
}

func Int() *Type     { return &Type{Kind: KindInt} }
func Float() *Type   { return &Type{Kind: KindFloat} }
func Str() *Type     { return &Type{Kind: KindString} }
func Boolean() *Type { return &Type{Kind: KindBool} }
func Top() *Type     { return &Type{Kind: KindTop} }
func Bottom() *Type  { return &Type{Kind: KindBottom} }

// Ref 构造对命名类型的引用。引用是叶子节点，是否已登记在构造时不检查。
func Ref(name string) *Type { return &Type{Kind: KindRef, Name: name} }

// Obj 构造对象类型。属性名重复属于参数非法，在登记/判定时报告。
func Obj(props ...Prop) *Type { return &Type{Kind: KindObject, Props: props} }

// Fn 构造函数类型。
func Fn(params []*Type, ret *Type) *Type {
	return &Type{Kind: KindFunc, Params: params, Ret: ret}
}

// Union 构造联合类型并做规范化：拍平嵌套联合、丢弃底类型成员、
// 含顶类型则吸收为顶类型、空联合归约为底类型、单成员联合归约为该成员。
func Union(members ...*Type) *Type {
	var flat []*Type
	hasTop := false
	var add func(m *Type)
	add = func(m *Type) {
		switch m.Kind {
		case KindUnion:
			for _, sub := range m.Members {
				add(sub)
			}
		case KindBottom:
		case KindTop:
			hasTop = true
		default:
			flat = append(flat, m)
		}
	}
	for _, m := range members {
		add(m)
	}
	if hasTop {
		return Top()
	}
	switch len(flat) {
	case 0:
		return Bottom()
	case 1:
		return flat[0]
	}
	return &Type{Kind: KindUnion, Members: flat}
}

// children 返回直接子表达式（不展开引用）。
func (t *Type) children() []*Type {
	switch t.Kind {
	case KindObject:
		out := make([]*Type, 0, len(t.Props))
		for _, p := range t.Props {
			out = append(out, p.Type)
		}
		return out
	case KindFunc:
		out := make([]*Type, 0, len(t.Params)+1)
		out = append(out, t.Params...)
		return append(out, t.Ret)
	case KindUnion:
		return t.Members
	}
	return nil
}

// Equal 报告两个类型表达式是否结构相同。
func Equal(a, b *Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Name != b.Name {
		return false
	}
	switch a.Kind {
	case KindObject:
		if len(a.Props) != len(b.Props) {
			return false
		}
		for i := range a.Props {
			pa, pb := a.Props[i], b.Props[i]
			if pa.Name != pb.Name || pa.Optional != pb.Optional || pa.ReadOnly != pb.ReadOnly {
				return false
			}
			if !Equal(pa.Type, pb.Type) {
				return false
			}
		}
	case KindFunc:
		if len(a.Params) != len(b.Params) || !Equal(a.Ret, b.Ret) {
			return false
		}
		for i := range a.Params {
			if !Equal(a.Params[i], b.Params[i]) {
				return false
			}
		}
	case KindUnion:
		if len(a.Members) != len(b.Members) {
			return false
		}
		for i := range a.Members {
			if !Equal(a.Members[i], b.Members[i]) {
				return false
			}
		}
	}
	return true
}

func (t *Type) String() string {
	switch t.Kind {
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindBool:
		return "bool"
	case KindTop:
		return "top"
	case KindBottom:
		return "bottom"
	case KindRef:
		return t.Name
	case KindObject:
		var b strings.Builder
		b.WriteString("{")
		for i, p := range t.Props {
			if i > 0 {
				b.WriteString(", ")
			}
			if p.ReadOnly {
				b.WriteString("readonly ")
			}
			b.WriteString(p.Name)
			if p.Optional {
				b.WriteString("?")
			}
			b.WriteString(": ")
			b.WriteString(p.Type.String())
		}
		b.WriteString("}")
		return b.String()
	case KindFunc:
		var b strings.Builder
		b.WriteString("(")
		for i, p := range t.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.String())
		}
		b.WriteString(") -> ")
		b.WriteString(t.Ret.String())
		return b.String()
	case KindUnion:
		parts := make([]string, len(t.Members))
		for i, m := range t.Members {
			parts[i] = m.String()
		}
		return strings.Join(parts, " | ")
	}
	return "<invalid>"
}

// findProp 按名字查找对象属性。
func findProp(t *Type, name string) (Prop, bool) {
	for _, p := range t.Props {
		if p.Name == name {
			return p, true
		}
	}
	return Prop{}, false
}

// primLE 是基本类型之间的子类型序：int <= float，其余仅自反。
func primLE(a, b Kind) bool {
	if a == b {
		return true
	}
	return a == KindInt && b == KindFloat
}
