package ontology

import "strings"

// Project 按规则集 rs 裁剪 obj，返回满足 sch 结构约束的独立副本。
//
// 语义要点：
//   - 规则只匹配与其段数相同的路径，单层通配不跨层；
//   - 祖先被自身命中的拒绝规则隐藏时，后代一律不可见；
//   - 容器字段只要仍有可见后代就保留，裁剪后变空的嵌套对象被移除；
//   - 不修改 obj，返回值的嵌套 map 与切片均为独立副本。
//
// sch 可为 nil（无结构约束）。必填属性被裁掉时返回 *RequiredFieldError；
// 依赖来源不可见而结果可见时按 sch.Policy 隐藏结果或返回 *DependencyError。
func Project(obj map[string]any, rs *RuleSet, sch *Schema) (map[string]any, error) {
	if rs == nil {
		rs = &RuleSet{}
	}
	out := projectMap(nil, obj, rs)
	if sch == nil {
		return out, nil
	}
	if err := applyDependencies(out, sch); err != nil {
		return nil, err
	}
	for _, req := range sch.Required {
		if !hasPath(out, strings.Split(req, ".")) {
			exp := rs.Explain(req)
			return nil, &RequiredFieldError{Path: req, Rule: exp.Rule, Reason: exp.Reason}
		}
	}
	return out, nil
}

// projectMap 递归裁剪一层对象。map 类型的子字段总是先递归（容器自身
// 被默认隐藏不阻断后代求值，但祖先拒绝规则会在 Explain 中覆盖后代），
// 结果为空则整个子树消失；标量与切片按自身可见性决定取舍。
func projectMap(path []string, obj map[string]any, rs *RuleSet) map[string]any {
	out := make(map[string]any, len(obj))
	for key, val := range obj {
		child := childPath(path, key)
		switch typed := val.(type) {
		case map[string]any:
			sub := projectMap(child, typed, rs)
			if len(sub) > 0 {
				out[key] = sub
			}
		case []any:
			if rs.Visible(strings.Join(child, ".")) {
				out[key] = copyValue(typed)
			}
		default:
			if rs.Visible(strings.Join(child, ".")) {
				out[key] = val
			}
		}
	}
	return out
}

// childPath 返回独立的子路径切片，避免共享底层数组。
func childPath(path []string, key string) []string {
	child := make([]string, len(path)+1)
	copy(child, path)
	child[len(path)] = key
	return child
}

// copyValue 深拷贝 map 与切片，标量按值返回。
func copyValue(val any) any {
	switch typed := val.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = copyValue(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = copyValue(v)
		}
		return out
	default:
		return val
	}
}

// applyDependencies 处理「来源不可见而结果可见」的依赖冲突。
func applyDependencies(out map[string]any, sch *Schema) error {
	for _, dep := range sch.Dependencies {
		result := strings.Split(dep.Result, ".")
		source := strings.Split(dep.Source, ".")
		if !hasPath(out, result) || hasPath(out, source) {
			continue
		}
		if sch.Policy == FailOnHiddenSource {
			return &DependencyError{Result: dep.Result, Source: dep.Source}
		}
		deletePath(out, result)
		pruneEmptyAncestors(out, result[:len(result)-1])
	}
	return nil
}

// hasPath 报告点分路径在嵌套 map 中是否存在。
func hasPath(obj map[string]any, segs []string) bool {
	cur := obj
	for i, seg := range segs {
		val, ok := cur[seg]
		if !ok {
			return false
		}
		if i == len(segs)-1 {
			return true
		}
		cur, ok = val.(map[string]any)
		if !ok {
			return false
		}
	}
	return true
}

// deletePath 删除点分路径指向的字段；路径不存在时无操作。
func deletePath(obj map[string]any, segs []string) {
	cur := obj
	for i, seg := range segs {
		if i == len(segs)-1 {
			delete(cur, seg)
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

// pruneEmptyAncestors 自底向上移除因删除而变空的嵌套对象。
func pruneEmptyAncestors(obj map[string]any, segs []string) {
	for i := len(segs); i > 0; i-- {
		parent, ok := lookupMap(obj, segs[:i-1])
		if !ok {
			return
		}
		node, ok := parent[segs[i-1]].(map[string]any)
		if !ok || len(node) > 0 {
			return
		}
		delete(parent, segs[i-1])
	}
}

// lookupMap 返回路径指向的嵌套 map。
func lookupMap(obj map[string]any, segs []string) (map[string]any, bool) {
	cur := obj
	for _, seg := range segs {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
