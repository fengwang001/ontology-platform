package projection

// Project 将规则集应用到 obj，返回裁剪后的独立副本。
// obj 不会被修改；返回值的嵌套 map 与 slice 均为新分配的副本。
// RuleSet 不可变，本方法可并发调用。
func (rs *RuleSet) Project(obj map[string]any) (map[string]any, error) {
	out := rs.projectMap(nil, obj)
	if err := rs.applyDependencies(out); err != nil {
		return nil, err
	}
	if err := rs.checkRequired(out); err != nil {
		return nil, err
	}
	return out, nil
}

// projectMap 逐字段裁剪一层对象；被拒绝的字段连同其子树整体消失。
func (rs *RuleSet) projectMap(path []string, m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		child := appendPath(path, k)
		if rs.decide(child).effect == Deny {
			continue
		}
		if pv, keep := rs.projectValue(child, v); keep {
			out[k] = pv
		}
	}
	return out
}

// projectValue 投影单个值；嵌套对象的所有子字段被裁掉时整体消失。
func (rs *RuleSet) projectValue(path []string, v any) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		sub := rs.projectMap(path, m)
		if len(sub) == 0 && len(m) > 0 {
			return nil, false
		}
		return sub, true
	}
	return cloneValue(v), true
}

// applyDependencies 处理计算来源不可见的派生属性。
func (rs *RuleSet) applyDependencies(out map[string]any) error {
	for _, dep := range rs.dependencies {
		if _, visible := out[dep.Field]; !visible {
			continue
		}
		for _, src := range dep.Sources {
			if _, ok := out[src]; ok {
				continue
			}
			if rs.onHidden == FailOnHiddenSource {
				return &DependencyError{Field: dep.Field, Source: src}
			}
			delete(out, dep.Field)
			break
		}
	}
	return nil
}

// checkRequired 校验必填属性在投影结果中仍然可见。
func (rs *RuleSet) checkRequired(out map[string]any) error {
	for _, field := range rs.required {
		if _, ok := out[field]; ok {
			continue
		}
		return &RequiredFieldError{Field: field, Rule: rs.Explain(field).Rule}
	}
	return nil
}

// appendPath 返回追加了 key 的新路径切片，不复用底层数组。
func appendPath(path []string, key string) []string {
	next := make([]string, len(path)+1)
	copy(next, path)
	next[len(path)] = key
	return next
}
