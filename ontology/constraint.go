package ontology

// Constraint 描述字段取值约束。各成员为空（nil / 0）表示该维度不受限。
// 该语言被刻意设计为包含关系可判定的子集。
type Constraint struct {
	Min       *float64 // 数值下界（含），仅对 int/float 有意义
	Max       *float64 // 数值上界（含）
	MaxLength int      // 字符串最大长度，0 表示不限
	Enum      []Value  // 允许取值集合，nil 表示不限
}

func fptr(v float64) *float64 { return &v }

// NumRange 构造数值区间约束的便捷函数。
func NumRange(min, max float64) Constraint {
	return Constraint{Min: fptr(min), Max: fptr(max)}
}

// EnumConstraint 构造枚举约束的便捷函数。
func EnumConstraint(vals ...Value) Constraint {
	return Constraint{Enum: vals}
}

// SatisfiedBy 精确判定取值是否满足约束。
func (c Constraint) SatisfiedBy(v Value) bool {
	if c.Min != nil || c.Max != nil {
		var f float64
		switch v.Kind {
		case KindInt:
			f = float64(v.I)
		case KindFloat:
			f = v.F
		default:
			return false
		}
		if c.Min != nil && f < *c.Min {
			return false
		}
		if c.Max != nil && f > *c.Max {
			return false
		}
	}
	if c.MaxLength > 0 {
		if v.Kind != KindString || len(v.S) > c.MaxLength {
			return false
		}
	}
	if c.Enum != nil {
		found := false
		for _, e := range c.Enum {
			if e.Equal(v) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Implies 判定 c 是否严格不宽于 o：凡满足 c 的取值必满足 o。
// 即 c 的解集是 o 解集的子集。
func (c Constraint) Implies(o Constraint) bool {
	if o.Min != nil {
		if c.Min == nil || *c.Min < *o.Min {
			return false
		}
	}
	if o.Max != nil {
		if c.Max == nil || *c.Max > *o.Max {
			return false
		}
	}
	if o.MaxLength > 0 {
		if c.MaxLength <= 0 || c.MaxLength > o.MaxLength {
			return false
		}
	}
	if o.Enum != nil {
		if c.Enum == nil {
			return false
		}
		for _, e := range c.Enum {
			found := false
			for _, oe := range o.Enum {
				if e.Equal(oe) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// Equal 判定两个约束解集相同（互相蕴含）。
func (c Constraint) Equal(o Constraint) bool {
	return c.Implies(o) && o.Implies(c)
}

// IsZero 判定是否为无约束。
func (c Constraint) IsZero() bool {
	return c.Min == nil && c.Max == nil && c.MaxLength == 0 && c.Enum == nil
}
