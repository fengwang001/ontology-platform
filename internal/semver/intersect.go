package semver

// Intersect 计算多个范围的逻辑交集。
//
// 各输入范围的约束分组被原样保留，因此结果的 Match 与
// “对每个输入范围分别 Match 再取与”完全等价。
// 若上下界互相排斥，返回 *ConflictError，指出冲突的两条原始约束。
func Intersect(rs ...Range) (Range, error) {
	out := Range{}
	for _, r := range rs {
		if len(r.groups) > 0 {
			out.groups = append(out.groups, r.groups...)
		}
	}
	if err := checkConflict(out.all()); err != nil {
		return Range{}, err
	}
	return out, nil
}

// checkConflict 只比较下界（>=/>）与上界（<=/<），找出第一对相互排斥的约束。
// 同向约束之间不可能冲突。
func checkConflict(cs []constraint) error {
	// 按约束在输入中的出现顺序报告冲突的双方。
	for i := range cs {
		for j := i + 1; j < len(cs); j++ {
			low, up, ok := asLowerUpper(cs[i], cs[j])
			if ok && boundsConflict(low, up) {
				return &ConflictError{Left: cs[i].Raw, Right: cs[j].Raw}
			}
		}
	}
	return nil
}

func asLowerUpper(a, b constraint) (low, up constraint, ok bool) {
	switch {
	case a.Op.isLower() && b.Op.isUpper():
		return a, b, true
	case a.Op.isUpper() && b.Op.isLower():
		return b, a, true
	default:
		return constraint{}, constraint{}, false
	}
}

func (o op) isLower() bool { return o == opGE || o == opGT }

func (o op) isUpper() bool { return o == opLE || o == opLT }

// boundsConflict 判定 low（下界）与 up（上界）是否互斥。
func boundsConflict(low, up constraint) bool {
	c := Compare(low.Bound, up.Bound)
	switch {
	case c > 0:
		// 下界高于上界，例如 >=1.3.0 与 <1.3.0。
		return true
	case c < 0:
		return false
	default:
		// 同一边界版本：仅当任一边是严格不等时冲突；
		// >=1.2.3 与 <=1.2.3 交于单点，不冲突。
		return low.Op == opGT || up.Op == opLT
	}
}

// MustIntersect 在交集为空时 panic，仅用于初始化期确定无冲突的场景。
func MustIntersect(rs ...Range) Range {
	r, err := Intersect(rs...)
	if err != nil {
		panic(err)
	}
	return r
}
