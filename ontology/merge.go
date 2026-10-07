package ontology

import "slices"

// 本文件是主实现的规则合并与裁决逻辑。语义规范见 DESIGN.md：
//
//   - 覆盖替代：覆盖条目 o 完全替代所有被其包含（subsumes(o, d)）的默认条目 d；
//     未被完全覆盖的默认条目继续沿用。
//   - 粒度不一致：部分重叠但互不包含的条目在判定阶段按“最高具体度”裁决；
//     最高具体度条目效果一致则结果唯一，否则按条目来源层级报告
//     ErrOverrideConflict（全部来自覆盖层）或 ErrMergeNonUnique。

// layeredEntry 是一条带层级来源标记的生效规则条目。
type layeredEntry struct {
	layer Layer
	entry RuleEntry
}

// subsumes 报告 a 的覆盖范围是否完全包含 b：
// Action 必须相等，其余维度 a 为通配或与 b 相同。
func subsumes(a, b RuleEntry) bool {
	return a.Action == b.Action &&
		(a.Role == "" || a.Role == b.Role) &&
		(a.Attribute == "" || a.Attribute == b.Attribute) &&
		(a.Predicate == "" || a.Predicate == b.Predicate)
}

// overlap 报告 a 与 b 的覆盖范围是否存在交集（可能共同作用于某个判定单元）。
func overlap(a, b RuleEntry) bool {
	compat := func(x, y string) bool { return x == "" || y == "" || x == y }
	return a.Action == b.Action &&
		compat(a.Role, b.Role) &&
		compat(a.Attribute, b.Attribute) &&
		compat(a.Predicate, b.Predicate)
}

// sameKey 报告两条目是否具有完全相同的范围键。
func sameKey(a, b RuleEntry) bool {
	return a.Action == b.Action && a.Role == b.Role &&
		a.Attribute == b.Attribute && a.Predicate == b.Predicate
}

// isRelaxation 以保守方式判定覆盖条目 o 是否构成对默认规则的放宽：
// o 允许了默认规则下可能被拒的某个判定单元。
// 宁可多报（要求更多授权依据），绝不少报。
func isRelaxation(o RuleEntry, defaults []RuleEntry) bool {
	if o.Effect != Allow {
		return false
	}
	subsumedAllow := false
	overlapDeny := false
	for _, d := range defaults {
		if d.Effect == Allow && subsumes(d, o) {
			subsumedAllow = true
		}
		if d.Effect == Deny && overlap(o, d) {
			overlapDeny = true
		}
	}
	return !subsumedAllow || overlapDeny
}

// checkInternalConflict 校验一个规则集声明内部是否存在
// 范围键完全相同但效果相反的条目（无法调和的内部冲突）。
// 相同键相同效果的重复条目被去重。返回去重后的条目列表。
func checkInternalConflict(entries []RuleEntry) ([]RuleEntry, error) {
	seen := make(map[RuleEntry]Effect, len(entries))
	keyOf := func(e RuleEntry) RuleEntry {
		return RuleEntry{Role: e.Role, Action: e.Action, Attribute: e.Attribute, Predicate: e.Predicate}
	}
	var out []RuleEntry
	for _, e := range entries {
		k := keyOf(e)
		if eff, ok := seen[k]; ok {
			if eff != e.Effect {
				return nil, newError(ErrOverrideConflict,
					"rule set declares conflicting effects (action=%q attribute=%q predicate=%q role=%q)",
					e.Action, e.Attribute, e.Predicate, e.Role)
			}
			continue
		}
		seen[k] = e.Effect
		out = append(out, e)
	}
	return out, nil
}

// mergeLayers 合并全局默认层与租户覆盖层：
// 删除所有被某个覆盖条目完全包含的默认条目，其余默认条目与全部覆盖条目共同生效。
// 返回生效条目（带来源层级）与被替代的默认条目数。
func mergeLayers(defaults *defaultRules, overrides *overrideRules) ([]layeredEntry, int) {
	var defEntries, ovrEntries []RuleEntry
	if defaults != nil {
		defEntries = defaults.entries
	}
	if overrides != nil {
		ovrEntries = overrides.entries
	}
	merged := make([]layeredEntry, 0, len(defEntries)+len(ovrEntries))
	subsumed := 0
	for _, d := range defEntries {
		replaced := false
		for _, o := range ovrEntries {
			if subsumes(o, d) {
				replaced = true
				break
			}
		}
		if replaced {
			subsumed++
		} else {
			merged = append(merged, layeredEntry{layer: LayerDefault, entry: d})
		}
	}
	for _, o := range ovrEntries {
		merged = append(merged, layeredEntry{layer: LayerOverride, entry: o})
	}
	return merged, subsumed
}

// applicable 报告条目是否适用于当前判定单元。
// 未在类型定义中登记的谓词按不成立处理（封闭世界，安全方向）。
func applicable(e RuleEntry, def ObjectTypeDef, inst Instance, subj Subject, action Action, attribute string) bool {
	if e.Action != action {
		return false
	}
	if e.Role != "" && !slices.Contains(subj.Roles, e.Role) {
		return false
	}
	if e.Attribute != "" && e.Attribute != attribute {
		return false
	}
	if e.Predicate != "" {
		p, ok := def.Predicates[e.Predicate]
		if !ok || !p(inst, subj) {
			return false
		}
	}
	return true
}

// maximalApplicable 返回适用于当前判定单元、且具体度最高的生效条目集合。
// 条目 e 被条目 f 压制当且仅当 f 的范围严格小于 e（subsumes(e, f) 且键不同）。
func maximalApplicable(merged []layeredEntry, def ObjectTypeDef, inst Instance, subj Subject, action Action, attribute string) []layeredEntry {
	var apps []layeredEntry
	for _, le := range merged {
		if applicable(le.entry, def, inst, subj, action, attribute) {
			apps = append(apps, le)
		}
	}
	var maximal []layeredEntry
outer:
	for _, e := range apps {
		for _, f := range apps {
			if !sameKey(e.entry, f.entry) && subsumes(e.entry, f.entry) {
				continue outer
			}
		}
		maximal = append(maximal, e)
	}
	return maximal
}

func uniformEffect(entries []layeredEntry) bool {
	for _, e := range entries[1:] {
		if e.entry.Effect != entries[0].entry.Effect {
			return false
		}
	}
	return true
}

func allFromLayer(entries []layeredEntry, layer Layer) bool {
	for _, e := range entries {
		if e.layer != layer {
			return false
		}
	}
	return true
}

func toWinning(entries []layeredEntry) []WinningEntry {
	out := make([]WinningEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, WinningEntry{Layer: e.layer, Entry: e.entry})
	}
	return out
}
