package orphanreclaim

import "sort"

// verdict 是一次孤儿判定的完整结果，供队列状态机与日志共同使用。
// 「进入待回收」与「脱离待回收」复用且仅复用这一套判定，标准完全一致。
func (v verdict) basis() RetentionBasis {
	switch {
	case v.independent != "":
		return RetentionBasis{Layer: "independent", LinkTypes: []string{v.independent}}
	case v.jointGroup != nil:
		return RetentionBasis{Layer: "joint", LinkTypes: append([]string(nil), v.jointGroup...)}
	default:
		return RetentionBasis{Layer: "none"}
	}
}

type verdict struct {
	orphan      bool
	independent string   // 命中的独立保留类型（第一层，空表示未命中）
	jointGroup  []string // 命中的联合保留完整组（第二层，nil 表示未命中）

	// 本次核对触及的类型桶数量。仅与配置中的独立类型数 + 联合组数有关，
	// 与对象全部入边总数无关，是复杂度不变量的可验证依据。
	bucketsChecked int
}

// evaluate 按固定两层次序判定：先任意独立保留类型，再联合保留组。
func (r *Reclaimer) evaluate(obj *objectState) verdict {
	v := verdict{orphan: true}

	// 第一层：任一独立保留类型存在入边即非孤儿，核对到首个命中即停止。
	for _, t := range r.norm.independents {
		v.bucketsChecked++
		if obj.inCounts[t] > 0 {
			v.orphan = false
			v.independent = t
			return v
		}
	}

	// 第二层：联合组要求组内每个类型都同时存在入边；组按代表键去重并稳定排序。
	keys := make([]string, 0, len(r.norm.jointGroups))
	seen := map[string]bool{}
	for _, g := range r.norm.jointGroups {
		if !seen[g[0]] {
			seen[g[0]] = true
			keys = append(keys, g[0])
		}
	}
	sort.Strings(keys)
	var groups [][]string
	for _, key := range keys {
		groups = append(groups, r.norm.jointGroups[key])
	}
	for _, g := range groups {
		present := true
		for _, t := range g {
			v.bucketsChecked++
			if obj.inCounts[t] == 0 {
				present = false
				break
			}
		}
		if present {
			v.orphan = false
			v.jointGroup = append([]string(nil), g...)
			return v
		}
	}
	return v
}
