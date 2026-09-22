package zone

// Stats 是一个行组的轻量统计。
// HasValue 为假表示行组内全部为空值，此时 Min/Max 无意义（"无"）。
// Min/Max 只统计非空值，空值绝不参与。
type Stats struct {
	Rows     int
	Nulls    int
	Min      int64
	Max      int64
	HasValue bool
}

// nonNull 返回非空值个数。
func (s Stats) nonNull() int { return s.Rows - s.Nulls }

// MayMatch 是裁剪判定式：报告行组是否"可能命中"谓词合取。
// 返回假时才允许跳过该行组。判定是保守的：
// 只在统计上不可能命中时才返回假，绝不误杀。
func (s Stats) MayMatch(c Conjunction) bool {
	for _, p := range c.Preds {
		if !s.mayMatchPred(p) {
			return false
		}
	}
	return true
}

// mayMatchPred 判定单个谓词是否可能命中。
//
// 形式化依据（设行组非空值集合 S，min<=v<=max 对一切 v∈S 成立）：
//   - Eq(v)：v∉[min,max] ⇒ ∀x∈S: x≠v，可排除。
//   - Lt(v)：min>=v ⇒ ∀x∈S: x>=v，可排除。
//   - Le(v)：min>v ⇒ ∀x∈S: x>v，可排除。
//   - Gt(v)：max<=v ⇒ ∀x∈S: x<=v，可排除。
//   - Ge(v)：max<v ⇒ ∀x∈S: x<v，可排除。
//   - In(V)：V∩[min,max]=∅ ⇒ ∀x∈S: x∉V，可排除。
//   - IsNull：Nulls=0 ⇒ 无空值，可排除。
//   - IsNotNull：非空值个数=0 ⇒ 无非空值，可排除。
//
// 边界（v==min 或 v==max）时上述前提均不成立，故不能排除：
// 等于 min/max 的值本身就可能命中。
func (s Stats) mayMatchPred(p Predicate) bool {
	switch p.Op {
	case OpIsNull:
		return s.Nulls > 0
	case OpIsNotNull:
		return s.nonNull() > 0
	}
	// 数值谓词只可能被非空值命中；全空行组直接排除。
	if !s.HasValue {
		return false
	}
	switch p.Op {
	case OpEq:
		return p.Value >= s.Min && p.Value <= s.Max
	case OpLt:
		return s.Min < p.Value
	case OpLe:
		return s.Min <= p.Value
	case OpGt:
		return s.Max > p.Value
	case OpGe:
		return s.Max >= p.Value
	case OpIn:
		for _, v := range p.Values {
			if v >= s.Min && v <= s.Max {
				return true
			}
		}
		return false
	}
	return true // 未知算子：保守保留
}
