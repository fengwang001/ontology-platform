package semver

// Intersect 把多个范围求交，返回合并后的 Range。
// 交集为空时返回 *ConflictError（可用 errors.Is(err, ErrEmptyIntersection)
// 判定），并指明互相冲突的两条具体约束。
func Intersect(rs ...Range) (Range, error) {
	if len(rs) == 0 {
		return Range{}, &ParseError{Kind: ErrInvalidRange, Input: "", Msg: "Intersect requires at least one range"}
	}

	var groups []group
	var all []Constraint
	var owners []int
	for _, r := range rs {
		for _, g := range r.groups {
			groups = append(groups, g)
			owner := len(groups) - 1
			for _, c := range g.constraints {
				all = append(all, c)
				owners = append(owners, owner)
			}
		}
	}

	merged := Range{groups: groups}
	if _, ok := feasibilityWitness(groups, all); ok {
		return merged, nil
	}
	if a, b, found := pairwiseConflict(all, owners); found {
		return Range{}, &ConflictError{Left: a, Right: b}
	}
	// 理论上不可达：不可行时必存在互相冲突的约束对。保留兜底以防万一。
	return Range{}, &ConflictError{Left: all[0], Right: all[len(all)-1]}
}

// MustIntersect 求交，失败时 panic，适合测试与初始化场景。
func MustIntersect(rs ...Range) Range {
	r, err := Intersect(rs...)
	if err != nil {
		panic(err)
	}
	return r
}
