// Package rng 提供单条约束区间的解析、求交与空区间判定，仅依赖 ver。
package rng

import (
	"errors"
	"strings"

	"ontology/ver"
)

// ErrInvalidConstraint 表示约束字符串语法非法，可判定（errors.Is）。
var ErrInvalidConstraint = errors.New("invalid constraint string")

// Bound 是区间的一个端点。Inclusive 为 false 时该端点开。
type Bound struct {
	V         ver.Version
	Inclusive bool
}

// Range 是下界与上界的合取；Has==false 表示该侧无界。
type Range struct {
	HasLow, HasHigh bool
	Low, High       Bound
}

// Origin 记录一条约束从哪条依赖边而来，用于冲突链回溯。
type Origin struct {
	Pkg    string      // 声明该约束的包；空表示根需求
	V      ver.Version // 声明该约束的版本
	Target string      // 被约束的包
	Raw    string      // 原始约束文本
}

// Entry 是一条进入某包域内的约束及其来源。
type Entry struct {
	Range  Range
	Origin Origin
}

// Any 是无任何限制的区间（"*"）。
func Any() Range { return Range{} }

// Parse 解析形如 ">=1.2.0 <2.0.0" 的空白分隔合取约束；"*" 表示任意。
func Parse(s string) (Range, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return Any(), nil
	}
	r := Range{}
	for _, atom := range strings.Fields(s) {
		if len(atom) < 2 {
			return Range{}, ErrInvalidConstraint
		}
		op := atom[:2]
		rest := atom[2:]
		one := false
		switch op {
		case ">=", "<=":
			one = true
		}
		if !one {
			switch atom[0] {
			case '>', '<', '=':
				op = atom[:1]
				rest = atom[1:]
			default:
				return Range{}, ErrInvalidConstraint
			}
		}
		v, err := ver.Parse(rest)
		if err != nil {
			return Range{}, ErrInvalidConstraint
		}
		incl := op == ">=" || op == "<=" || op == "="
		switch {
		case op[0] == '>':
			b := Bound{v, incl}
			if !r.HasLow || tighterLow(b, r.Low) {
				r.HasLow, r.Low = true, b
			}
		default: // '<' 或 '='
			if op == "=" {
				r = r.Intersect(Range{
					HasLow: true, Low: Bound{v, true},
					HasHigh: true, High: Bound{v, true},
				})
			} else {
				b := Bound{v, incl}
				if !r.HasHigh || tighterHigh(b, r.High) {
					r.HasHigh, r.High = true, b
				}
			}
		}
	}
	return r, nil
}

// Intersect 返回两个区间的交。
func (r Range) Intersect(o Range) Range {
	out := r
	if o.HasLow {
		if !out.HasLow || tighterLow(o.Low, out.Low) {
			out.HasLow, out.Low = true, o.Low
		}
	}
	if o.HasHigh {
		if !out.HasHigh || tighterHigh(o.High, out.High) {
			out.HasHigh, out.High = true, o.High
		}
	}
	return out
}

// Empty 报告区间在版本全序上是否结构性为空（与登记内容无关）。
func (r Range) Empty() bool {
	if !r.HasLow || !r.HasHigh {
		return false
	}
	c := r.Low.V.Compare(r.High.V)
	return c > 0 || c == 0 && (!r.Low.Inclusive || !r.High.Inclusive)
}

// Contains 报告版本 v 是否落入区间。
func (r Range) Contains(v ver.Version) bool {
	if r.HasLow {
		c := v.Compare(r.Low.V)
		if c < 0 || c == 0 && !r.Low.Inclusive {
			return false
		}
	}
	if r.HasHigh {
		c := v.Compare(r.High.V)
		if c > 0 || c == 0 && !r.High.Inclusive {
			return false
		}
	}
	return true
}

func (r Range) String() string {
	var parts []string
	if r.HasLow {
		op := ">"
		if r.Low.Inclusive {
			op = ">="
		}
		parts = append(parts, op+r.Low.V.String())
	}
	if r.HasHigh {
		op := "<"
		if r.High.Inclusive {
			op = "<="
		}
		parts = append(parts, op+r.High.V.String())
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " ")
}

// tighterLow 报告 a 是否比 b 更严格（更靠右；同点开比闭严）。
func tighterLow(a, b Bound) bool {
	c := a.V.Compare(b.V)
	return c > 0 || c == 0 && !a.Inclusive && b.Inclusive
}

// tighterHigh 报告 a 是否比 b 更严格（更靠左；同点开比闭严）。
func tighterHigh(a, b Bound) bool {
	c := a.V.Compare(b.V)
	return c < 0 || c == 0 && !a.Inclusive && b.Inclusive
}
