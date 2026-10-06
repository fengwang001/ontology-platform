package ontology

// narrow.go 只负责“已知合法的条件”如何把一个环境切分为真/假两个环境。
// 静态错误（未声明变量、不可访问属性、缺少判别属性等）由 analyzer 在调用
// splitEnv 之前按优先级判定；本文件中的函数不再返回这些错误。

// env 是程序点上的窄化环境：变量名 -> 规范化类型。
// nil 表示不可达环境。
type env map[string]*normType

func cloneEnv(e env) env {
	if e == nil {
		return nil
	}
	c := make(env, len(e))
	for k, v := range e {
		c[k] = v
	}
	return c
}

func updateVar(e env, name string, n *normType) env {
	c := cloneEnv(e)
	c[name] = n
	return c
}

// unionEnv 逐变量取并集；两个环境都不可达（nil）时结果不可达。
func unionEnv(a, b env) env {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	c := make(env, len(a)+len(b))
	for k, v := range a {
		c[k] = v
	}
	for k, v := range b {
		if old, ok := c[k]; ok {
			// 永不类型是底（bottom）：never ∪ T = T。
			if old.isNever() {
				c[k] = v
				continue
			}
			if v.isNever() {
				c[k] = old
				continue
			}
			c[k] = unionNorm(old, v)
		} else {
			c[k] = v
		}
	}
	return c
}

// splitEnv 按条件把可达环境切成真环境与假环境。任一结果可能因被条件作用的
// 那个变量窄化为永不而变成 nil（不可达）；其它变量未被该条件作用，不参与
// 可达性判定。
func splitEnv(e env, cond *Cond) (env, env) {
	t := cloneEnv(e)
	f := cloneEnv(e)
	var target string
	switch cond.K {
	case CondIsType:
		target = cond.Var
		t[cond.Var] = keepTypeOf(t[cond.Var], cond.Check)
		f[cond.Var] = dropTypeOf(f[cond.Var], cond.Check)
	case CondEqNull:
		target = cond.Var
		t[cond.Var] = onlyKind(t[cond.Var], lkNull)
		f[cond.Var] = dropExactLeaf(f[cond.Var], leaf{kind: lkNull})
	case CondEqUndefined:
		target = cond.Var
		t[cond.Var] = onlyKind(t[cond.Var], lkUndefined)
		f[cond.Var] = dropExactLeaf(f[cond.Var], leaf{kind: lkUndefined})
	case CondLooseNull:
		target = cond.Var
		t[cond.Var] = keepKinds(t[cond.Var], lkNull, lkUndefined)
		f[cond.Var] = dropKinds(f[cond.Var], lkNull, lkUndefined)
	case CondEqLiteral:
		target = cond.Var
		ln := mustNormalize(cond.Lit)
		lit := ln.members[0] // 分析器已保证是单一字面量
		t[cond.Var] = narrowEqLiteralTrue(t[cond.Var], lit)
		f[cond.Var] = narrowEqLiteralFalse(f[cond.Var], lit)
	case CondTruthy:
		target = cond.Var
		t[cond.Var] = narrowTruthyTrue(t[cond.Var])
		f[cond.Var] = narrowTruthyFalse(f[cond.Var])
	case CondDiscrim:
		target = cond.Var
		ln := mustNormalize(cond.Lit)
		lit := ln.members[0]
		t[cond.Var] = narrowDiscrimTrue(t[cond.Var], cond.Prop, lit)
		f[cond.Var] = narrowDiscrimFalse(f[cond.Var], cond.Prop, lit)
	case CondNot:
		innerT, innerF := splitEnv(e, cond.Inner)
		// 取反：真环境是子条件的假环境，假环境是子条件的真环境。
		return finalize(innerF, cond), finalize(innerT, cond)
	case CondAnd:
		t, f := splitAnd(e, cond)
		return finalize(t, cond), finalize(f, cond)
	case CondOr:
		t, f := splitOr(e, cond)
		return finalize(t, cond), finalize(f, cond)
	}
	return reachableIfVar(t, target), reachableIfVar(f, target)
}

// finalize 按条件作用到的全部变量判定分支环境是否可达：
// 任一被作用变量被窄化为永不，该分支即没有具体取值可满足，环境不可达。
func finalize(e env, cond *Cond) env {
	if e == nil {
		return nil
	}
	touched := map[string]bool{}
	collectVars(cond, touched)
	for variable := range touched {
		n, ok := e[variable]
		if !ok || n == nil || n.isNever() {
			return nil
		}
	}
	return e
}

// reachableIfVar 按单个被条件作用的变量判定可达性。
func reachableIfVar(e env, variable string) env {
	if e == nil {
		return nil
	}
	n, ok := e[variable]
	if !ok || n == nil || n.isNever() {
		return nil
	}
	return e
}

func splitAnd(e env, cond *Cond) (env, env) {
	lt, lf := splitEnv(e, cond.Left)
	var rt, rf env
	if lt != nil {
		rt, rf = splitEnv(lt, cond.Right)
	}
	// 真：左真 且 右真。
	truth := rt
	// 假：左假（右条件不求值，其余变量维持入口）与（左真 且 右假）逐变量并集。
	falsity := unionEnv(lf, rf)
	return truth, falsity
}

func splitOr(e env, cond *Cond) (env, env) {
	lt, lf := splitEnv(e, cond.Left)
	var rt, rf env
	if lf != nil {
		rt, rf = splitEnv(lf, cond.Right)
	}
	// 真：左真 或（左假 且 右真），逐变量并集。
	truth := unionEnv(lt, rt)
	// 假：左假 且 右假。
	falsity := rf
	return truth, falsity
}

// splitRightOnBasis 以入口环境 base 为底切分右条件：右条件作用的变量取
// active 中的当前窄化类型，未作用到的变量在结果环境里保留 base 入口类型。
func splitRightOnBasis(base, active env, right *Cond) (env, env) {
	if active == nil {
		// 右条件不会被求值：两个结果环境都是不可达的“空世界”。
		return nil, nil
	}
	t, f := splitEnv(active, right)
	return rebaseEnv(base, t, right), rebaseEnv(base, f, right)
}

func rebaseEnv(base, e env, c *Cond) env {
	if e == nil {
		return nil
	}
	touched := map[string]bool{}
	collectVars(c, touched)
	// 完整环境：未被右条件作用的变量取入口 base 的值（短路另一支未执行右条件），
	// 被作用的变量取右条件求值后的窄化结果 e。
	out := cloneEnv(base)
	for name := range touched {
		if v, ok := e[name]; ok {
			out[name] = v
		}
	}
	return out
}

func collectVars(c *Cond, out map[string]bool) {
	switch c.K {
	case CondAnd, CondOr:
		collectVars(c.Left, out)
		collectVars(c.Right, out)
	case CondNot:
		collectVars(c.Inner, out)
	default:
		if c.Var != "" {
			out[c.Var] = true
		}
	}
}

// ---- 成员级过滤 ----

func filterMembers(n *normType, keep func(leaf) bool) *normType {
	var out []leaf
	for _, lf := range n.members {
		if keep(lf) {
			out = append(out, lf)
		}
	}
	return canonical(out)
}

func keepTypeOf(n *normType, c CheckedType) *normType {
	switch c {
	case CheckNumber:
		return filterMembers(n, func(l leaf) bool { return l.kind == lkNumber || l.kind == lkNumLit })
	case CheckString:
		return filterMembers(n, func(l leaf) bool { return l.kind == lkString || l.kind == lkStrLit })
	case CheckBoolean:
		return filterMembers(n, func(l leaf) bool { return l.kind == lkTrue || l.kind == lkFalse })
	case CheckUndefined:
		return filterMembers(n, func(l leaf) bool { return l.kind == lkUndefined })
	case CheckObject:
		// typeof null === "object"
		return filterMembers(n, func(l leaf) bool { return l.kind == lkObject || l.kind == lkNull })
	}
	return n
}

func dropTypeOf(n *normType, c CheckedType) *normType {
	switch c {
	case CheckNumber:
		return filterMembers(n, func(l leaf) bool { return l.kind != lkNumber && l.kind != lkNumLit })
	case CheckString:
		return filterMembers(n, func(l leaf) bool { return l.kind != lkString && l.kind != lkStrLit })
	case CheckBoolean:
		return filterMembers(n, func(l leaf) bool { return l.kind != lkTrue && l.kind != lkFalse })
	case CheckUndefined:
		return filterMembers(n, func(l leaf) bool { return l.kind != lkUndefined })
	case CheckObject:
		return filterMembers(n, func(l leaf) bool { return l.kind != lkObject && l.kind != lkNull })
	}
	return n
}

func keepKinds(n *normType, kinds ...leafKind) *normType {
	set := map[leafKind]bool{}
	for _, k := range kinds {
		set[k] = true
	}
	return filterMembers(n, func(l leaf) bool { return set[l.kind] })
}

func dropKinds(n *normType, kinds ...leafKind) *normType {
	set := map[leafKind]bool{}
	for _, k := range kinds {
		set[k] = true
	}
	return filterMembers(n, func(l leaf) bool { return !set[l.kind] })
}

func onlyKind(n *normType, k leafKind) *normType {
	return filterMembers(n, func(l leaf) bool { return l.kind == k })
}

func dropExactLeaf(n *normType, target leaf) *normType {
	key := leafKey(target)
	return filterMembers(n, func(l leaf) bool { return leafKey(l) != key })
}

// narrowEqLiteralTrue：与字面量严格相等为真。
func narrowEqLiteralTrue(n *normType, lit leaf) *normType {
	var out []leaf
	for _, lf := range n.members {
		switch lit.kind {
		case lkNumLit:
			if lf.kind == lkNumLit && lf.num == lit.num {
				out = append(out, lf)
			} else if lf.kind == lkNumber {
				out = append(out, lit)
			}
		case lkStrLit:
			if lf.kind == lkStrLit && lf.str == lit.str {
				out = append(out, lf)
			} else if lf.kind == lkString {
				out = append(out, lit)
			}
		case lkTrue, lkFalse:
			if lf.kind == lit.kind {
				out = append(out, lf)
			}
		case lkNull:
			if lf.kind == lkNull {
				out = append(out, lf)
			}
		case lkUndefined:
			if lf.kind == lkUndefined {
				out = append(out, lf)
			}
		}
	}
	return canonical(out)
}

// narrowEqLiteralFalse：与字面量严格相等为假——只删去相同字面量成员，
// 对应原子类型成员保持不变（布尔由两个字面量构成，故可删去其一）。
func narrowEqLiteralFalse(n *normType, lit leaf) *normType {
	return filterMembers(n, func(lf leaf) bool {
		switch lit.kind {
		case lkNumLit:
			return !(lf.kind == lkNumLit && lf.num == lit.num)
		case lkStrLit:
			return !(lf.kind == lkStrLit && lf.str == lit.str)
		case lkTrue, lkFalse:
			return lf.kind != lit.kind
		case lkNull:
			return lf.kind != lkNull
		case lkUndefined:
			return lf.kind != lkUndefined
		}
		return true
	})
}

// narrowTruthyTrue：真值判断为真。
func narrowTruthyTrue(n *normType) *normType {
	return filterMembers(n, func(lf leaf) bool {
		switch lf.kind {
		case lkNull, lkUndefined, lkFalse:
			return false
		case lkNumLit:
			return lf.num != 0
		case lkStrLit:
			return lf.str != ""
		default:
			// 原子数字、原子字符串与对象成员保留；true 保留
			return true
		}
	})
}

// narrowTruthyFalse：真值判断为假。
func narrowTruthyFalse(n *normType) *normType {
	var out []leaf
	for _, lf := range n.members {
		switch lf.kind {
		case lkNull, lkUndefined, lkFalse:
			out = append(out, lf)
		case lkNumber:
			out = append(out, leaf{kind: lkNumLit, num: 0})
		case lkString:
			out = append(out, leaf{kind: lkStrLit, str: ""})
		case lkNumLit:
			if lf.num == 0 {
				out = append(out, lf)
			}
		case lkStrLit:
			if lf.str == "" {
				out = append(out, lf)
			}
		}
		// true 与对象成员删去
	}
	return canonical(out)
}

// narrowDiscrimTrue：判别属性为真，只留属性类型包含该字面量的对象成员。
func narrowDiscrimTrue(n *normType, prop string, lit leaf) *normType {
	return filterMembers(n, func(lf leaf) bool {
		if lf.kind != lkObject {
			return false
		}
		pt, ok := lf.props[prop]
		return ok && normHasLeaf(pt, lit)
	})
}

// narrowDiscrimFalse：判别属性为假，只删去属性类型恰为该单一字面量的对象成员。
func narrowDiscrimFalse(n *normType, prop string, lit leaf) *normType {
	return filterMembers(n, func(lf leaf) bool {
		if lf.kind != lkObject {
			return false
		}
		pt, ok := lf.props[prop]
		if !ok {
			return true // 分析期已保证所有对象成员都有该属性，这里保持保守
		}
		if len(pt.members) == 1 && leafKey(pt.members[0]) == leafKey(lit) {
			return false
		}
		return true
	})
}

func normHasLeaf(n *normType, target leaf) bool {
	key := leafKey(target)
	for _, lf := range n.members {
		if leafKey(lf) == key {
			return true
		}
	}
	return false
}

func mustNormalize(t *Type) *normType {
	n, err := normalize(t)
	if err != nil {
		panic(err)
	}
	return n
}
