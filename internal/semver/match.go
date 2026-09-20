package semver

// Match 判定版本是否落在范围内。
//
// 预发布版本只有在“某一个输入范围”中存在这样一条约束——其操作数与该版本
// major/minor/patch 完全相同且操作数自身带预发布——时，才可能被该范围匹配；
// 同时它必须满足该输入范围的全部约束。不同输入范围的门控不互相借用。
func (r Range) Match(v Version) bool {
	for _, group := range r.groups {
		if !matchGroup(v, group) {
			return false
		}
	}
	return true
}

func matchGroup(v Version, cs []constraint) bool {
	if v.HasPre() && !prereleaseAllowed(v, cs) {
		return false
	}
	for _, c := range cs {
		if !c.match(v) {
			return false
		}
	}
	return true
}

// prereleaseAllowed 检查本组中是否存在同三元组且自身带预发布的操作数。
func prereleaseAllowed(v Version, cs []constraint) bool {
	for _, c := range cs {
		o := c.Operand
		if o.HasPre() && o.Major == v.Major && o.Minor == v.Minor && o.Patch == v.Patch {
			return true
		}
	}
	return false
}

// match 按比较操作符判定版本（不含预发布门控）。
func (c constraint) match(v Version) bool {
	cmp := Compare(v, c.Bound)
	switch c.Op {
	case opGE:
		return cmp >= 0
	case opGT:
		return cmp > 0
	case opLE:
		return cmp <= 0
	case opLT:
		return cmp < 0
	default: // opEQ：= 仅在优先级相同时成立，build metadata 不影响。
		return cmp == 0
	}
}

// MatchAny 报告 v 是否被任一给定范围匹配（逻辑或，便于调用方使用）。
func MatchAny(v Version, rs ...Range) bool {
	for _, r := range rs {
		if r.Match(v) {
			return true
		}
	}
	return false
}
