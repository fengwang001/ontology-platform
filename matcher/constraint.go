package matcher

import (
	"fmt"

	"ontology/graph"
)

// Op 是属性约束的比较算子。
type Op string

// 支持的约束算子。
const (
	OpEq Op = "=="
	OpNe Op = "!="
	OpGt Op = ">"
	OpLt Op = "<"
)

// Constraint 是针对节点单个属性的约束。
type Constraint struct {
	Attr string
	Op   Op
	// Value 是比较右值，支持 string / int / int64 / float64 / bool。
	Value any
}

// validate 检查约束自身是否合法，并返回静态校验错误。
func (c Constraint) validate() error {
	if c.Attr == "" {
		return fmt.Errorf("%w: constraint attribute must not be empty", ErrPatternDefinition)
	}
	switch c.Op {
	case OpEq, OpNe, OpGt, OpLt:
	default:
		return fmt.Errorf("%w: unknown constraint operator %q", ErrPatternDefinition, c.Op)
	}
	if !comparableKind(c.Value) {
		return fmt.Errorf("%w: constraint value for %q has unsupported type %T", ErrPatternDefinition, c.Attr, c.Value)
	}
	return nil
}

// matches 判断对象属性是否满足约束。缺失属性一律不满足。
func (c Constraint) matches(o *graph.Object) bool {
	actual, ok := o.Attributes[c.Attr]
	if !ok {
		return false
	}
	switch c.Op {
	case OpEq:
		return equalValue(actual, c.Value)
	case OpNe:
		return !equalValue(actual, c.Value)
	case OpGt, OpLt:
		a, aOK := asFloat(actual)
		b, bOK := asFloat(c.Value)
		if !aOK || !bOK {
			return false
		}
		if c.Op == OpGt {
			return a > b
		}
		return a < b
	default:
		return false
	}
}

func comparableKind(v any) bool {
	switch v.(type) {
	case string, bool, int, int64, float64:
		return true
	default:
		return false
	}
}

// equalValue 采用规范化数值比较（int/int64/float64 数值相等即相等），
// 其余类型要求动态类型与值均相同。
func equalValue(a, b any) bool {
	if af, ok := asFloat(a); ok {
		if bf, ok := asFloat(b); ok {
			return af == bf
		}
		return false
	}
	return a == b
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
