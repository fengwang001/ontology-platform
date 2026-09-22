// Package zone 定义行组统计（min/max/空值计数）与谓词裁剪判定。
//
// 三值逻辑：空值参与任何数值比较的结果都是 UNKNOWN（非真非假），
// 因此空值不命中任何数值谓词，只被 IS NULL 命中。
// 裁剪判定是"可能命中"的保守判定：只允许排除统计上
// 不可能命中的行组，绝不误杀可能命中的行组。
package zone

// Op 是谓词比较运算符。
type Op int

const (
	OpEq Op = iota
	OpLt
	OpLe
	OpGt
	OpGe
	OpIn
	OpIsNull
	OpIsNotNull
)

// Predicate 是单个列谓词。
type Predicate struct {
	Op     Op
	Value  int64   // Eq/Lt/Le/Gt/Ge 的比较值
	Values []int64 // In 的候选集合
}

// Eq 构造 = v 谓词。
func Eq(v int64) Predicate { return Predicate{Op: OpEq, Value: v} }

// Lt 构造 < v 谓词。
func Lt(v int64) Predicate { return Predicate{Op: OpLt, Value: v} }

// Le 构造 <= v 谓词。
func Le(v int64) Predicate { return Predicate{Op: OpLe, Value: v} }

// Gt 构造 > v 谓词。
func Gt(v int64) Predicate { return Predicate{Op: OpGt, Value: v} }

// Ge 构造 >= v 谓词。
func Ge(v int64) Predicate { return Predicate{Op: OpGe, Value: v} }

// In 构造 IN 谓词。
func In(vs ...int64) Predicate { return Predicate{Op: OpIn, Values: vs} }

// IsNull 构造 IS NULL 谓词。
func IsNull() Predicate { return Predicate{Op: OpIsNull} }

// IsNotNull 构造 IS NOT NULL 谓词。
func IsNotNull() Predicate { return Predicate{Op: OpIsNotNull} }

// Match 判定单个值是否命中谓词。isNull 为真时：
// IS NULL 命中，IS NOT NULL 不命中，其余一切数值谓词均为 UNKNOWN（不命中）。
func (p Predicate) Match(v int64, isNull bool) bool {
	if isNull {
		return p.Op == OpIsNull
	}
	switch p.Op {
	case OpEq:
		return v == p.Value
	case OpLt:
		return v < p.Value
	case OpLe:
		return v <= p.Value
	case OpGt:
		return v > p.Value
	case OpGe:
		return v >= p.Value
	case OpIn:
		for _, c := range p.Values {
			if v == c {
				return true
			}
		}
		return false
	case OpIsNull:
		return false
	case OpIsNotNull:
		return true
	}
	return false
}

// And 构造 AND 组合谓词（空集合恒真）。
func And(ps ...Predicate) Conjunction { return Conjunction{Preds: ps} }

// Conjunction 是若干谓词的 AND 组合。
type Conjunction struct {
	Preds []Predicate
}

// Match 判定单个值是否命中全部谓词。
func (c Conjunction) Match(v int64, isNull bool) bool {
	for _, p := range c.Preds {
		if !p.Match(v, isNull) {
			return false
		}
	}
	return true
}
