package subtype

import "sort"

// validateExpr 校验类型表达式本身是否合法：非 nil，
// 且（递归地）同一对象内属性名不重复。
func validateExpr(t Type) error {
	switch t := t.(type) {
	case nil:
		return &Error{Kind: KindInvalidArgument, Detail: "nil type expression"}
	case Int, Float, Str, Bool, Top, Bottom:
		return nil
	case Object:
		seen := make(map[string]bool, len(t.Props))
		for _, p := range t.Props {
			if seen[p.Name] {
				return &Error{Kind: KindInvalidArgument, Detail: "duplicate property name: " + p.Name}
			}
			seen[p.Name] = true
			if err := validateExpr(p.Type); err != nil {
				return err
			}
		}
		return nil
	case Func:
		for _, p := range t.Params {
			if err := validateExpr(p); err != nil {
				return err
			}
		}
		return validateExpr(t.Return)
	case Union:
		for _, m := range t.Members {
			if err := validateExpr(m); err != nil {
				return err
			}
		}
		return nil
	case Ref:
		return nil
	}
	return &Error{Kind: KindInvalidArgument, Detail: "unknown type expression"}
}

// reachableNames 收集从 roots 出发（经已登记定义展开）静态可达的全部命名类型名。
// 未定义的名字也会被收集（但没有定义可继续展开）。
func reachableNames(defs map[string]Type, roots ...Type) map[string]bool {
	reach := make(map[string]bool)
	var walk func(t Type)
	walk = func(t Type) {
		switch t := t.(type) {
		case Object:
			for _, p := range t.Props {
				walk(p.Type)
			}
		case Func:
			for _, p := range t.Params {
				walk(p)
			}
			walk(t.Return)
		case Union:
			for _, m := range t.Members {
				walk(m)
			}
		case Ref:
			if reach[t.Name] {
				return
			}
			reach[t.Name] = true
			if def, ok := defs[t.Name]; ok {
				walk(def)
			}
		}
	}
	for _, root := range roots {
		walk(root)
	}
	return reach
}

// findUndefined 返回可达但未定义的字典序最小名字。
func findUndefined(defs map[string]Type, reach map[string]bool) (string, bool) {
	var missing []string
	for name := range reach {
		if _, ok := defs[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return "", false
	}
	sort.Strings(missing)
	return missing[0], true
}

// unguardedRefs 收集 t 中不经过对象或函数构造子即可到达的引用名字：
// 定义顶层与联合成员是“无保护”位置，对象属性与函数参数/返回值是“有保护”位置。
func unguardedRefs(t Type, out map[string]bool) {
	switch t := t.(type) {
	case Ref:
		out[t.Name] = true
	case Union:
		for _, m := range t.Members {
			unguardedRefs(m, out)
		}
	}
}

// findUnguardedCycle 在可达且已定义的命名类型中检测无保护循环，
// 返回处于某个无保护回环上的字典序最小名字。
func findUnguardedCycle(defs map[string]Type, reach map[string]bool) (string, bool) {
	// 构造无保护引用图：name -> 其定义中无保护位置直接引用的名字。
	edges := make(map[string]map[string]bool)
	for name := range reach {
		def, ok := defs[name]
		if !ok {
			continue
		}
		targets := make(map[string]bool)
		unguardedRefs(def, targets)
		edges[name] = targets
	}
	// 对每个可达名字做 DFS，判断它能否经无保护边回到自身。
	var cyclic []string
	for start := range edges {
		if reachesSelf(edges, start) {
			cyclic = append(cyclic, start)
		}
	}
	if len(cyclic) == 0 {
		return "", false
	}
	sort.Strings(cyclic)
	return cyclic[0], true
}

// reachesSelf 判断从 start 出发沿无保护边能否再回到 start。
func reachesSelf(edges map[string]map[string]bool, start string) bool {
	visited := make(map[string]bool)
	stack := []string{start}
	first := true
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == start && !first {
			return true
		}
		first = false
		if visited[n] {
			continue
		}
		visited[n] = true
		for next := range edges[n] {
			stack = append(stack, next)
		}
	}
	return false
}
