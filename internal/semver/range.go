package semver

import "strings"

// op 是单条比较约束的操作符。
type op int

const (
	opGE op = iota // >=
	opGT           // >
	opLE           // <=
	opLT           // <
	opEQ           // =
)

func (o op) String() string {
	switch o {
	case opGE:
		return ">="
	case opGT:
		return ">"
	case opLE:
		return "<="
	case opLT:
		return "<"
	default:
		return "="
	}
}

// constraint 是一条原子比较约束。Bound 是展开 ^/~ 后的比较版本。
type constraint struct {
	Op    op
	Bound Version
	// Operand 是用户书写的操作数（如 ^0.2.3 的 0.2.3）。
	Operand Version
	// Raw 是这条约束的原始写法（含操作符），用于冲突解释。
	Raw string
}

func (c constraint) String() string { return c.Raw }

// Range 是若干条约束的逻辑与；groups 保留每个 ParseRange 输入的分组，
// 预发布许可判定按组进行（见 match.go）。
type Range struct {
	groups [][]constraint
}

// Constraints 返回展开后、按输入范围分组的全部约束副本。
func (r Range) Constraints() [][]constraint {
	out := make([][]constraint, len(r.groups))
	copy(out, r.groups)
	return out
}

// all 返回所有分组中的约束（顺序扁平）。
func (r Range) all() []constraint {
	var cs []constraint
	for _, g := range r.groups {
		cs = append(cs, g...)
	}
	return cs
}

// ParseRange 解析空格分隔的多条约束（逻辑与）。
func ParseRange(s string) (Range, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Range{}, ErrEmptyRange
	}
	cs := make([]constraint, 0, len(fields))
	for _, f := range fields {
		c, err := parseConstraint(f)
		if err != nil {
			return Range{}, &RangeParseError{Input: s, Reason: err.Error()}
		}
		cs = append(cs, c...)
	}
	return Range{groups: [][]constraint{cs}}, nil
}

func rangeFail(reason string) error { return stringError(reason) }

// parseConstraint 把一条书写形式展开为 1~2 条原子约束。
func parseConstraint(f string) ([]constraint, error) {
	if f == "" {
		return nil, rangeFail("empty constraint")
	}
	operator := ""
	rest := f
	switch {
	case strings.HasPrefix(f, ">="):
		operator, rest = ">=", f[2:]
	case strings.HasPrefix(f, "<="):
		operator, rest = f[:2], f[2:]
	case strings.HasPrefix(f, ">"), strings.HasPrefix(f, "<"),
		strings.HasPrefix(f, "="), strings.HasPrefix(f, "^"),
		strings.HasPrefix(f, "~"):
		operator, rest = f[:1], f[1:]
	default:
		return nil, rangeFail("missing or unknown operator in: " + f)
	}

	v, err := Parse(rest)
	if err != nil {
		return nil, err
	}
	mk := func(o op, bound Version) constraint {
		return constraint{Op: o, Bound: bound, Operand: v, Raw: f}
	}

	switch {
	case operator == ">=":
		return []constraint{mk(opGE, v)}, nil
	case operator == "<=":
		return []constraint{mk(opLE, v)}, nil
	case operator == ">":
		return []constraint{mk(opGT, v)}, nil
	case operator == "<":
		return []constraint{mk(opLT, v)}, nil
	case operator == "=":
		return []constraint{mk(opEQ, v)}, nil
	case operator == "~":
		return []constraint{
			mk(opGE, v),
			mk(opLT, Version{Major: v.Major, Minor: v.Minor + 1, Patch: 0}),
		}, nil
	case operator == "^":
		return caretConstraints(v, mk), nil
	}
	return nil, rangeFail("unsupported constraint: " + f)
}

func caretConstraints(v Version, mk func(op, Version) constraint) []constraint {
	upper := Version{Major: v.Major + 1, Minor: 0, Patch: 0}
	switch {
	case v.Major > 0:
	case v.Minor > 0:
		upper = Version{Major: 0, Minor: v.Minor + 1, Patch: 0}
	case true:
		upper = Version{Major: 0, Minor: 0, Patch: v.Patch + 1}
	}
	return []constraint{mk(opGE, v), mk(opLT, upper)}
}
