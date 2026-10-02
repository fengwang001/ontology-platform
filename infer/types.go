package infer

import (
	"fmt"
	"strings"
)

// Type 是一个类型项：要么是类型变量 Var(id)（id 从 1 起），
// 要么是构造子应用 Con(name, args)。Type 是值类型，可安全复制。
type Type struct {
	IsVar bool
	ID    int
	Name  string
	Args  []Type
}

// Var 构造编号为 id 的类型变量。
func Var(id int) Type {
	return Type{IsVar: true, ID: id}
}

// Con 构造给定构造子名字与参数的应用。
func Con(name string, args ...Type) Type {
	return Type{Name: name, Args: append([]Type(nil), args...)}
}

// String 返回类型的可打印形式，如 "Fn(Var(1), List(Var(2)))"。
// 模式内部的量化变量以负编号存储，打印为 G0、G1……。
func (t Type) String() string {
	if t.IsVar {
		if t.ID < 0 {
			return fmt.Sprintf("G%d", -t.ID-1)
		}
		return fmt.Sprintf("Var(%d)", t.ID)
	}
	if len(t.Args) == 0 {
		return t.Name
	}
	parts := make([]string, len(t.Args))
	for i, a := range t.Args {
		parts[i] = a.String()
	}
	return fmt.Sprintf("%s(%s)", t.Name, strings.Join(parts, ", "))
}

// clone 返回 t 的深拷贝。
func (t Type) clone() Type {
	c := t
	if t.Args != nil {
		c.Args = make([]Type, len(t.Args))
		for i, a := range t.Args {
			c.Args[i] = a.clone()
		}
	}
	return c
}

// mapVars 对 t 中每个变量应用 f，构造子结构保持不变。
func mapVars(t Type, f func(id int) Type) Type {
	if t.IsVar {
		return f(t.ID)
	}
	args := make([]Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = mapVars(a, f)
	}
	return Type{Name: t.Name, Args: args}
}
