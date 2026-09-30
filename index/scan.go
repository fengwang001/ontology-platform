package index

import "sort"

// Result 是一次扫描的结果。
type Result struct {
	Plan     Plan      // 推导出的访问路径
	Keys     [][]Value // 命中条目，按索引序输出
	Examined int       // 考察的条目数，恰等于快照中落在区间内的条目数
}

// matchCond 用单个条件判定一条键。
// 比较条件对空值永不成立，只有 IS NULL 能匹配空值。
func matchCond(c Cond, v Value) bool {
	if c.Op == OpIsNull {
		return v.Kind == KindNull
	}
	if v.Kind == KindNull {
		return false
	}
	switch c.Op {
	case OpEq:
		return compare(v, c.Values[0]) == 0
	case OpIn:
		return memberOf(v, dedupSort(c.Values))
	case OpLt:
		return compare(v, c.Values[0]) < 0
	case OpLe:
		return compare(v, c.Values[0]) <= 0
	case OpGt:
		return compare(v, c.Values[0]) > 0
	case OpGe:
		return compare(v, c.Values[0]) >= 0
	}
	return false
}

// MatchesAll 用全部条件逐行过滤，是对拍基准也是残余过滤的语义。
func (ix *Index) MatchesAll(conds []Cond, key []Value) bool {
	for _, c := range conds {
		i, ok := ix.colIdx[c.Column]
		if !ok || !matchCond(c, key[i]) {
			return false
		}
	}
	return true
}

// Scan 在扫描开始时刻的一致快照上执行查询。
// 只考察推导区间内的条目，区间未覆盖的列条件作残余过滤。
// 查询被拒绝时不改变任何统计。
func (ix *Index) Scan(q Query, limit int) (Result, error) {
	plan, err := ix.Derive(q, limit)
	if err != nil {
		return Result{}, err
	}
	snap := ix.snapshot()
	res := Result{Plan: plan}
	if plan.Empty {
		ix.recordScan(0, 0)
		return res, nil
	}
	for _, iv := range plan.Intervals {
		start := 0
		if !iv.Lo.Unbounded {
			start = sort.Search(len(snap), func(i int) bool {
				return !belowLo(snap[i], iv.Lo)
			})
		}
		end := len(snap)
		if !iv.Hi.Unbounded {
			end = sort.Search(len(snap), func(i int) bool {
				return aboveHi(snap[i], iv.Hi)
			})
		}
		for i := start; i < end; i++ {
			res.Examined++
			if ix.MatchesAll(plan.Residual, snap[i]) {
				res.Keys = append(res.Keys, snap[i])
			}
		}
	}
	ix.recordScan(res.Examined, len(res.Keys))
	return res, nil
}
