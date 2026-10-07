package ontology

import "sort"

// Rule 是属性取值规则：一个有限的允许取值集合。
// 取值域为字符串，规则即允许值集合，子集约束可直接判定。
type Rule struct {
	allowed map[string]struct{}
}

// NewRule 由允许值列表构造规则。空集合表示不允许任何取值。
func NewRule(allowed []string) Rule {
	set := make(map[string]struct{}, len(allowed))
	for _, v := range allowed {
		set[v] = struct{}{}
	}
	return Rule{allowed: set}
}

// Allows 判定值是否被规则允许。
func (r Rule) Allows(value string) bool {
	_, ok := r.allowed[value]
	return ok
}

// IsSubsetOf 判定 r 是否为 other 的子集或相等。
func (r Rule) IsSubsetOf(other Rule) bool {
	for v := range r.allowed {
		if _, ok := other.allowed[v]; !ok {
			return false
		}
	}
	return true
}

// Equal 判定两条规则的允许集合是否完全一致。
func (r Rule) Equal(other Rule) bool {
	return len(r.allowed) == len(other.allowed) && r.IsSubsetOf(other)
}

// Values 返回排序后的允许值列表，便于比较与日志输出。
func (r Rule) Values() []string {
	out := make([]string, 0, len(r.allowed))
	for v := range r.allowed {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (r Rule) clone() Rule {
	set := make(map[string]struct{}, len(r.allowed))
	for v := range r.allowed {
		set[v] = struct{}{}
	}
	return Rule{allowed: set}
}
