package ontology

import (
	"fmt"
	"strconv"
	"strings"
)

// eval.go 实现默认规则与覆盖规则的确定性合并与访问判定。
//
// 合并规则（对每一个判定点，即单个属性或单个谓词条件，独立适用）：
//  1. 覆盖层存在适用声明时，完全替代默认层；否则沿用默认层；两层都没有则隐式拒绝。
//  2. 同一层内，范围更窄（集合严格包含关系）的声明优先，保证粒度不一致时结果唯一确定。
//  3. 同一层内范围完全相同且效果矛盾 → ErrOverrideConflict（无法调和的内部冲突）。
//  4. 同一层内范围部分重叠但互不包含、且对交集效果矛盾 → ErrMergeAmbiguous（合并不唯一）。

// scopeMembers 将范围展开为判定点名称集合。universe 为该维度（属性或谓词）的全集。
func scopeMembers(sc Scope, universe []string) map[string]bool {
	switch sc.Kind {
	case ScopeAttr, ScopePredicate:
		return map[string]bool{sc.Name: true}
	case ScopeAttrSet, ScopePredicateSet:
		m := make(map[string]bool, len(sc.Members))
		for _, s := range sc.Members {
			m[s] = true
		}
		return m
	default: // 通配
		m := make(map[string]bool, len(universe))
		for _, s := range universe {
			m[s] = true
		}
		return m
	}
}

// isStrictSubset 报告 a 是否为 b 的严格子集。
func isStrictSubset(a, b map[string]bool) bool {
	if len(a) >= len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// resolved 是一层规则对单个判定点的裁决结果。
type resolved struct {
	effect Effect
	stmtID string
	found  bool
}

// resolveLayer 在单层规则内裁决一个判定点。
// layer 用于错误信息（"default" 或 "override"）。
func resolveLayer(stmts []Statement, universe []string, point, layer string) (resolved, error) {
	type cand struct {
		stmt Statement
		set  map[string]bool
	}
	var apps []cand
	for _, st := range stmts {
		set := scopeMembers(st.Scope, universe)
		if set[point] {
			apps = append(apps, cand{stmt: st, set: set})
		}
	}
	if len(apps) == 0 {
		return resolved{}, nil
	}
	// 窄范围优先：若存在另一条适用声明的范围是本声明范围的严格子集，则本声明被遮蔽。
	var mins []cand
	for i, c := range apps {
		shadowed := false
		for j, other := range apps {
			if i != j && isStrictSubset(other.set, c.set) {
				shadowed = true
				break
			}
		}
		if !shadowed {
			mins = append(mins, c)
		}
	}
	// 按范围集合分组：同组范围完全相同。
	var groups [][]cand
	for _, c := range mins {
		placed := false
		for gi := range groups {
			if sameSet(groups[gi][0].set, c.set) {
				groups[gi] = append(groups[gi], c)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []cand{c})
		}
	}
	// 组内必须一致：范围完全相同而效果矛盾 → 无法调和的内部冲突。
	groupEffects := make([]Effect, len(groups))
	groupIDs := make([]string, len(groups))
	for gi, g := range groups {
		groupEffects[gi] = g[0].stmt.Effect
		groupIDs[gi] = g[0].stmt.ID
		for _, c := range g[1:] {
			if c.stmt.Effect != groupEffects[gi] {
				return resolved{}, newError(ErrOverrideConflict,
					"%s 层规则对判定点 %q 存在无法调和的内部冲突：声明 %q 与 %q 范围相同但效果矛盾",
					layer, point, groupIDs[gi], c.stmt.ID)
			}
		}
	}
	// 组间范围互不包含（非嵌套部分重叠）：效果必须一致，否则合并结果不唯一。
	for gi := 1; gi < len(groups); gi++ {
		if groupEffects[gi] != groupEffects[0] {
			return resolved{}, newError(ErrMergeAmbiguous,
				"%s 层规则对判定点 %q 的合并结果不唯一：声明 %q 与 %q 范围粒度不一致且效果矛盾",
				layer, point, groupIDs[0], groupIDs[gi])
		}
	}
	return resolved{effect: groupEffects[0], stmtID: groupIDs[0], found: true}, nil
}

// splitByRow 将声明按行级/属性维度拆分。
func splitByRow(stmts []Statement) (attrs, rows []Statement) {
	for _, st := range stmts {
		if st.Scope.IsRow() {
			rows = append(rows, st)
		} else {
			attrs = append(attrs, st)
		}
	}
	return attrs, rows
}

// decidePoint 合并两层规则裁决单个判定点，并记录层级依据。
func decidePoint(defAttrs, defRows, ovrAttrs, ovrRows []Statement,
	attrUniverse, predUniverse []string, row bool, name string,
	basis map[string]string) (Effect, error) {
	key := "attr:" + name
	dLayer, oLayer, universe := defAttrs, ovrAttrs, attrUniverse
	if row {
		key = "pred:" + name
		dLayer, oLayer, universe = defRows, ovrRows, predUniverse
	}
	if r, err := resolveLayer(oLayer, universe, name, "override"); err != nil {
		return Deny, err
	} else if r.found {
		basis[key] = "override:" + r.stmtID
		return r.effect, nil
	}
	if r, err := resolveLayer(dLayer, universe, name, "default"); err != nil {
		return Deny, err
	} else if r.found {
		basis[key] = "default:" + r.stmtID
		return r.effect, nil
	}
	basis[key] = "implicit-deny"
	return Deny, nil
}

// evaluate 对一次访问请求在给定规则快照上求值。
// defaults 为全局默认规则，overrides 为实例归属租户的覆盖规则。
func evaluate(def *ObjectTypeDef, defaults, overrides []Statement, inst Instance) (Decision, error) {
	attrUniverse := append([]string(nil), def.Attrs...)
	predUniverse := make([]string, 0, len(def.Predicates))
	for _, p := range def.Predicates {
		predUniverse = append(predUniverse, p.ID)
	}
	defAttrs, defRows := splitByRow(defaults)
	ovrAttrs, ovrRows := splitByRow(overrides)

	basis := make(map[string]string)
	dec := Decision{
		Attrs: make(map[string]bool, len(def.Attrs)),
		Trace: Trace{
			DefaultEntriesExamined:  len(defaults),
			OverrideEntriesExamined: len(overrides),
			Basis:                   basis,
		},
	}

	// 先完成全部判定点的层级裁决；错误按固定优先级汇报：
	// ErrOverrideConflict 优先于 ErrMergeAmbiguous。
	type pointResult struct {
		key    string
		effect Effect
	}
	var conflictErr, ambErr error
	attrEffects := make(map[string]Effect, len(def.Attrs))
	for _, a := range def.Attrs {
		eff, err := decidePoint(defAttrs, defRows, ovrAttrs, ovrRows, attrUniverse, predUniverse, false, a, basis)
		if err != nil {
			collectErr(err, &conflictErr, &ambErr)
			continue
		}
		attrEffects[a] = eff
	}
	predEffects := make(map[string]Effect, len(def.Predicates))
	for _, p := range def.Predicates {
		eff, err := decidePoint(defAttrs, defRows, ovrAttrs, ovrRows, attrUniverse, predUniverse, true, p.ID, basis)
		if err != nil {
			collectErr(err, &conflictErr, &ambErr)
			continue
		}
		predEffects[p.ID] = eff
	}
	if conflictErr != nil {
		return Decision{}, conflictErr
	}
	if ambErr != nil {
		return Decision{}, ambErr
	}

	for _, a := range def.Attrs {
		dec.Attrs[a] = attrEffects[a] == Allow
	}
	// 行级判定：实例命中任一被允许的谓词条件即放行；类型未登记谓词时行级不受限。
	if len(def.Predicates) == 0 {
		dec.RowAllowed = true
		basis["@row"] = "no-predicates"
	} else {
		for _, p := range def.Predicates {
			if predEffects[p.ID] == Allow && matchPredicate(p, inst) {
				dec.RowAllowed = true
				basis["@row"] = "predicate:" + p.ID
				break
			}
		}
		if !dec.RowAllowed {
			basis["@row"] = "no-matching-allowed-predicate"
		}
	}
	return dec, nil
}

func collectErr(err error, conflictErr, ambErr *error) {
	var e *Error
	if !asError(err, &e) {
		return
	}
	switch e.Code {
	case ErrOverrideConflict:
		if *conflictErr == nil {
			*conflictErr = err
		}
	case ErrMergeAmbiguous:
		if *ambErr == nil {
			*ambErr = err
		}
	}
}

func asError(err error, target **Error) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*Error); ok {
		*target = e
		return true
	}
	return false
}

// matchPredicate 判断实例是否命中谓词条件。
func matchPredicate(p Predicate, inst Instance) bool {
	v, ok := inst.Attrs[p.Field]
	if !ok {
		return false
	}
	if fn, ok := asFloat(v); ok {
		if fc, ok := asFloat(p.Value); ok {
			switch p.Op {
			case "eq":
				return fn == fc
			case "ne":
				return fn != fc
			case "lt":
				return fn < fc
			case "le":
				return fn <= fc
			case "gt":
				return fn > fc
			case "ge":
				return fn >= fc
			}
			return false
		}
	}
	sv := fmt.Sprint(v)
	cv := fmt.Sprint(p.Value)
	switch p.Op {
	case "eq":
		return sv == cv
	case "ne":
		return sv != cv
	case "contains":
		return strings.Contains(sv, cv)
	}
	return false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}
