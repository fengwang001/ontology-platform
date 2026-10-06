package genericinst

import (
	"strconv"
	"strings"
)

// Constraint 描述一个类型形参的约束。只支持两种：
//   - OneOf：实参规范化后必须是给定已登记具名类型之一
//   - SameAs：实参规范化后必须与另一个形参的实参等价
type Constraint struct {
	OneOf  []string // 允许的具名类型 CanonKey 集合
	SameAs int      // 必须等价的另一个形参下标；-1 表示无此约束
}

// Param 是一个类型形参的声明。
type Param struct {
	Name    string      // 形参名
	Default Type        // 默认值；nil 表示无默认值，省略该实参即参数错误
	Con     *Constraint // nil 表示无约束
}

// Def 是一个已登记的泛型定义。
type Def struct {
	Name   string
	Params []Param
}

// normalizeArgs 对一次请求的类型实参做默认值补齐，返回与形参等长的实参切片。
// 实参个数超过形参个数、或省略了无默认值的形参，均返回 ErrArgument。
func normalizeArgs(d *Def, args []Type) ([]Type, *RequestError) {
	if len(args) > len(d.Params) {
		return nil, &RequestError{Code: ErrArgument,
			Msg: "too many type arguments: got " + itoa(len(args)) + ", want " + itoa(len(d.Params))}
	}
	out := make([]Type, len(d.Params))
	for i, p := range d.Params {
		if i < len(args) {
			out[i] = args[i]
			continue
		}
		if p.Default != nil {
			out[i] = p.Default // 省略时按默认值补齐后再比较
			continue
		}
		return nil, &RequestError{Code: ErrArgument,
			Msg: "missing type argument for parameter " + strconv.Quote(p.Name)}
	}
	return out, nil
}

// checkConstraints 在规范化后校验全部形参约束。
// 按形参声明顺序逐形参校验，使首个约束错误的位置确定。
func checkConstraints(d *Def, args []Type) *RequestError {
	for i, p := range d.Params {
		if p.Con == nil {
			continue
		}
		con := p.Con
		got := argKey(args[i])
		if len(con.OneOf) > 0 {
			ok := false
			for _, allowed := range con.OneOf {
				if got == argKey(NewNominal(allowed)) {
					ok = true
					break
				}
			}
			if !ok {
				return &RequestError{Code: ErrConstraint,
					Msg: "argument for " + strconv.Quote(p.Name) + " is not one of the declared types"}
			}
		}
		if con.SameAs >= 0 && con.SameAs < len(d.Params) {
			if got != argKey(args[con.SameAs]) {
				return &RequestError{Code: ErrConstraint,
					Msg: "argument for " + strconv.Quote(p.Name) +
						" must equal argument for " + strconv.Quote(d.Params[con.SameAs].Name)}
			}
		}
	}
	return nil
}

// argKey 生成单个类型实参的规范化键。
func argKey(t Type) string {
	return resolveAlias(t).CanonKey()
}

// argsKey 生成整个实参列表的规范化键，字段次序参与区分。
func argsKey(args []Type) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = argKey(a)
	}
	return strings.Join(parts, ";")
}

func quote(s string) string { return strconv.Quote(s) }

func itoa(n int) string { return strconv.Itoa(n) }
