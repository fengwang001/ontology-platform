// Package subtype 提供支持递归命名类型的结构化子类型判定。
//
// 类型表达式包括基本类型（整数、浮点、字符串、布尔）、顶类型、底类型、
// 对象类型、函数类型、联合类型以及对命名类型的引用。
package subtype

import "strings"

// Type 是类型表达式的封闭接口，只允许本包内的实现。
type Type interface {
	isType()
	String() string
}

// Int 整数类型，是 Float 的子类型。
type Int struct{}

// Float 浮点类型。
type Float struct{}

// Str 字符串类型。
type Str struct{}

// Bool 布尔类型。
type Bool struct{}

// Top 顶类型，任何类型都是它的子类型。
type Top struct{}

// Bottom 底类型，是任何类型的子类型；空联合等价于它。
type Bottom struct{}

// Prop 对象类型的一个属性。
type Prop struct {
	Name     string
	Type     Type
	Optional bool
	ReadOnly bool
}

// Object 对象类型，一组属性的集合。
type Object struct {
	Props []Prop
}

// Func 函数类型，由参数类型列表与返回类型构成。
type Func struct {
	Params []Type
	Return Type
}

// Union 联合类型；Members 为空时等价于 Bottom。
type Union struct {
	Members []Type
}

// Ref 对命名类型的引用。
type Ref struct {
	Name string
}

func (Int) isType()    {}
func (Float) isType()  {}
func (Str) isType()    {}
func (Bool) isType()   {}
func (Top) isType()    {}
func (Bottom) isType() {}
func (Object) isType() {}
func (Func) isType()   {}
func (Union) isType()  {}
func (Ref) isType()    {}

func (Int) String() string    { return "int" }
func (Float) String() string  { return "float" }
func (Str) String() string    { return "str" }
func (Bool) String() string   { return "bool" }
func (Top) String() string    { return "top" }
func (Bottom) String() string { return "bottom" }

func (p Prop) String() string {
	var b strings.Builder
	if p.ReadOnly {
		b.WriteString("ro ")
	}
	b.WriteString(p.Name)
	if p.Optional {
		b.WriteString("?")
	}
	b.WriteString(": ")
	b.WriteString(formatType(p.Type))
	return b.String()
}

func (o Object) String() string {
	parts := make([]string, len(o.Props))
	for i, p := range o.Props {
		parts[i] = p.String()
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

func (f Func) String() string {
	parts := make([]string, len(f.Params))
	for i, p := range f.Params {
		parts[i] = formatType(p)
	}
	return "(" + strings.Join(parts, ", ") + ") -> " + formatType(f.Return)
}

func (u Union) String() string {
	if len(u.Members) == 0 {
		return "bottom"
	}
	parts := make([]string, len(u.Members))
	for i, m := range u.Members {
		parts[i] = formatType(m)
	}
	return "(" + strings.Join(parts, " | ") + ")"
}

func (r Ref) String() string { return "@" + r.Name }

// formatType 为嵌套在其它类型中的类型表达式生成字符串。
func formatType(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

// P 构造一个必选、可写的属性。
func P(name string, t Type) Prop { return Prop{Name: name, Type: t} }

// Opt 构造一个可选、可写的属性。
func Opt(name string, t Type) Prop { return Prop{Name: name, Type: t, Optional: true} }

// RO 构造一个必选、只读的属性。
func RO(name string, t Type) Prop { return Prop{Name: name, Type: t, ReadOnly: true} }

// OptRO 构造一个可选、只读的属性。
func OptRO(name string, t Type) Prop {
	return Prop{Name: name, Type: t, Optional: true, ReadOnly: true}
}

// Obj 构造对象类型。
func Obj(props ...Prop) Object { return Object{Props: props} }

// Fn 构造函数类型。
func Fn(ret Type, params ...Type) Func { return Func{Params: params, Return: ret} }

// Or 构造联合类型。
func Or(members ...Type) Union { return Union{Members: members} }
