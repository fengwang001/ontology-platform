// Package zone 维护行组的轻量统计（min/max/空值计数）并提供谓词裁剪判定。
// 所有判定是“可能命中”的保守判定：只排除统计上不可能命中的行组。
package zone

// Stats 是一个行组的区带统计。空值不参与 min/max；全空行组 HasMin/HasMax=false。
type Stats struct {
	Count     int
	NullCount int
	Min       int64
	Max       int64
	HasMin    bool
	HasMax    bool
}

// FromValues 构建统计；present 为逐行“是否存在值”的位图（nil 表示全部存在）。
func FromValues(vals []int64, present []uint64) Stats {
	s := Stats{Count: len(vals)}
	for i := range vals {
		if present != nil && present[i/64]&(1<<uint(i%64)) == 0 {
			s.NullCount++
			continue
		}
		if !s.HasMin || vals[i] < s.Min {
			s.Min, s.HasMin = vals[i], true
		}
		if !s.HasMax || vals[i] > s.Max {
			s.Max, s.HasMax = vals[i], true
		}
	}
	if s.HasMin {
		s.HasMax = true
	}
	return s
}

// Op 是谓词算子。
type Op uint8

const (
	OpEq Op = iota + 1
	OpLt
	OpLe
	OpGt
	OpGe
	OpIn
	OpIsNull
	OpIsNotNull
)

// Cond 是单个谓词条件。IN 使用 In 列表；IS NULL / IS NOT NULL 忽略参数。
type Cond struct {
	Op  Op
	V   int64
	In  []int64
}

// Filter 是若干 Cond 的 AND。nil/空 Filter 恒真。
type Filter []Cond

func between(s Stats, v int64) bool {
	return s.HasMin && v >= s.Min && v <= s.Max
}

// Keeps 是统计裁剪判定：返回 true 表示该组“可能”有命中，不可排除。
func (f Filter) Keeps(s Stats) bool {
	for _, c := range f {
		switch c.Op {
		case OpEq:
			if !between(s, c.V) {
				return false
			}
		case OpLt:
			if !s.HasMin || s.Min >= c.V {
				return false
			}
		case OpLe:
			if !s.HasMin || s.Min > c.V {
				return false
			}
		case OpGt:
			if !s.HasMax || s.Max <= c.V {
				return false
			}
		case OpGe:
			if !s.HasMax || s.Max < c.V {
				return false
			}
		case OpIn:
			ok := false
			for _, v := range c.In {
				if between(s, v) {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		case OpIsNull:
			if s.NullCount == 0 {
				return false
			}
		case OpIsNotNull:
			if s.NullCount == s.Count {
				return false
			}
		}
	}
	return true
}

// Match 在具体一行上判定谓词。present=false（NULL）不命中任何数值谓词，
// 只被 IS NULL 命中；IS NOT NULL 只在 present=true 时成立。
func (f Filter) Match(v int64, present bool) bool {
	for _, c := range f {
		switch c.Op {
		case OpIsNull:
			if present {
				return false
			}
		case OpIsNotNull:
			if !present {
				return false
			}
		default:
			if !present {
				return false
			}
			switch c.Op {
			case OpEq:
				if v != c.V {
					return false
				}
			case OpLt:
				if v >= c.V {
					return false
				}
			case OpLe:
				if v > c.V {
					return false
				}
			case OpGt:
				if v <= c.V {
					return false
				}
			case OpGe:
				if v < c.V {
					return false
				}
			case OpIn:
				found := false
				for _, x := range c.In {
					if x == v {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
		}
	}
	return true
}
