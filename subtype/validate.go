package subtype

// validateExpr 校验单个类型表达式的结构合法性：
// 非空、Kind 已知、子表达式非空、属性名非空且不重复、表达式本身无指针环。
// 不检查引用是否已登记（那是判定期的职责）。
func validateExpr(t *Type) error {
	return validate(t, map[*Type]bool{})
}

func validate(t *Type, active map[*Type]bool) error {
	if t == nil {
		return invalidf("nil type expression")
	}
	if active[t] {
		return invalidf("cyclic type expression")
	}
	active[t] = true
	defer delete(active, t)
	switch t.Kind {
	case KindInt, KindFloat, KindString, KindBool, KindTop, KindBottom:
		return nil
	case KindRef:
		if t.Name == "" {
			return invalidf("empty name in type reference")
		}
		return nil
	case KindObject:
		names := map[string]bool{}
		for _, p := range t.Props {
			if p.Name == "" {
				return invalidf("empty property name in object type")
			}
			if names[p.Name] {
				return &Error{Kind: ErrInvalidArgument, Name: p.Name,
					Detail: "duplicate property name " + quote(p.Name)}
			}
			names[p.Name] = true
			if err := validate(p.Type, active); err != nil {
				return err
			}
		}
		return nil
	case KindFunc:
		for _, p := range t.Params {
			if err := validate(p, active); err != nil {
				return err
			}
		}
		if t.Ret == nil {
			return invalidf("function type with nil return type")
		}
		return validate(t.Ret, active)
	case KindUnion:
		for _, m := range t.Members {
			if err := validate(m, active); err != nil {
				return err
			}
		}
		return nil
	}
	return invalidf("unknown type kind")
}

// closure 计算从 roots 出发静态可达的全部类型节点与命名类型。
// 静态可达指穿越所有构造子（对象/函数/联合）并展开已登记引用的传递闭包。
// 返回：可达节点集合、可达名字集合、未登记名字列表（去重，无序）。
func closure(defs map[string]*Type, roots ...*Type) (nodes map[*Type]bool, names map[string]bool, undefined []string) {
	nodes = map[*Type]bool{}
	names = map[string]bool{}
	undefSeen := map[string]bool{}
	expanded := map[string]bool{}
	var walk func(t *Type)
	walk = func(t *Type) {
		if t == nil || nodes[t] {
			return
		}
		nodes[t] = true
		if t.Kind == KindRef {
			names[t.Name] = true
			if _, ok := defs[t.Name]; !ok {
				if !undefSeen[t.Name] {
					undefSeen[t.Name] = true
					undefined = append(undefined, t.Name)
				}
				return
			}
			if !expanded[t.Name] {
				expanded[t.Name] = true
				walk(defs[t.Name])
			}
			return
		}
		for _, c := range t.children() {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return nodes, names, undefined
}

// findUnguardedCycle 在 names 中的已登记名字里检测无保护循环：
// 某名字仅经联合与引用（不经过对象/函数构造子）展开回自身。
// 返回处于环上的字典序最小名字。
func findUnguardedCycle(defs map[string]*Type, names map[string]bool) (string, bool) {
	edges := map[string][]string{}
	for n := range names {
		def, ok := defs[n]
		if !ok {
			continue
		}
		out := map[string]bool{}
		var walk func(t *Type)
		walk = func(t *Type) {
			switch t.Kind {
			case KindUnion:
				for _, m := range t.Members {
					walk(m)
				}
			case KindRef:
				out[t.Name] = true
			}
		}
		walk(def)
		for m := range out {
			edges[n] = append(edges[n], m)
		}
	}
	best := ""
	for n := range edges {
		if reachesSelf(edges, n) && (best == "" || n < best) {
			best = n
		}
	}
	return best, best != ""
}

// reachesSelf 报告从 n 出发沿无保护边能否回到 n。
func reachesSelf(edges map[string][]string, n string) bool {
	visited := map[string]bool{}
	stack := append([]string(nil), edges[n]...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == n {
			return true
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		stack = append(stack, edges[cur]...)
	}
	return false
}
