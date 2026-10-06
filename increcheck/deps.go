package increcheck

// deps.go：依赖追踪。
//
// 依赖在检查过程中被动记录，一次检查完成后以「本次读取的签名集合」
// 整体替换该检查上一次的依赖（精确反映最新一次检查）。
// 两类依赖严格分开：
//   sigEdges  —— 签名检查 -> 读取过的签名，失效沿此图传递并成 SCC；
//   implEdges —— 实现检查 -> 读取过的签名，只用于定位失效的实现检查，
//               绝不向外传递（实现变化不影响他人）。
// 两张图都同时保存反向边，使「谁直接依赖我」是一次查表，
// 不随无关声明总数增长。

// DepGraph 保存两类依赖及其反向边。
type DepGraph struct {
	sigFwd  map[string]map[string]struct{} // owner -> 它依赖的签名
	sigRev  map[string]map[string]struct{} // 签名 -> 直接依赖它的签名检查
	implFwd map[string]map[string]struct{} // owner -> 其实现依赖的签名
	implRev map[string]map[string]struct{} // 签名 -> 直接依赖它的实现检查
}

func NewDepGraph() *DepGraph {
	return &DepGraph{
		sigFwd:  map[string]map[string]struct{}{},
		sigRev:  map[string]map[string]struct{}{},
		implFwd: map[string]map[string]struct{}{},
		implRev: map[string]map[string]struct{}{},
	}
}

// recordSig 用本次检查记录到的依赖集合整体替换 owner 的签名依赖。
func (g *DepGraph) recordSig(owner string, refs []string) {
	for dep := range g.sigFwd[owner] {
		delete(g.sigRev[dep], owner)
	}
	next := map[string]struct{}{}
	for _, ref := range refs {
		next[ref] = struct{}{}
		if g.sigRev[ref] == nil {
			g.sigRev[ref] = map[string]struct{}{}
		}
		g.sigRev[ref][owner] = struct{}{}
	}
	g.sigFwd[owner] = next
}

// recordImpl 记录 owner 的实现检查所读取的签名；只维护反向边，
// 因为实现依赖从不向外传递，不需要它的正向可达性。
func (g *DepGraph) recordImpl(owner string, refs []string) {
	for dep := range g.implFwd[owner] {
		delete(g.implRev[dep], implTag(owner))
	}
	next := map[string]struct{}{}
	for _, ref := range refs {
		next[ref] = struct{}{}
		if g.implRev[ref] == nil {
			g.implRev[ref] = map[string]struct{}{}
		}
		// owner 的实现检查反向挂到 ref 上；用 owner+impl 无法与同名
		// 签名检查冲突，因为 implDependents 独立查表，见 implSet。
		g.implRev[ref][implTag(owner)] = struct{}{}
	}
	g.implFwd[owner] = next
}

// implTag 给实现检查的反向边键加命名空间，避免与签名检查混淆。
func implTag(owner string) string { return "\x00impl:" + owner }

func implOwner(tag string) string { return tag[len("\x00impl:"):] }

func (g *DepGraph) dropSig(owner string) {
	g.recordSig(owner, nil)
}

func (g *DepGraph) dropImpl(owner string) {
	for dep := range g.implFwd[owner] {
		delete(g.implRev[dep], implTag(owner))
	}
	delete(g.implFwd, owner)
}

func (g *DepGraph) sigDepsOf(id string) []string {
	out := make([]string, 0, len(g.sigFwd[id]))
	for dep := range g.sigFwd[id] {
		out = append(out, dep)
	}
	return sortedStrings(out)
}

// sigDependents 返回直接依赖 id 签名的签名检查标识。
func (g *DepGraph) sigDependents(id string) []string {
	out := make([]string, 0, len(g.sigRev[id]))
	for owner := range g.sigRev[id] {
		out = append(out, owner)
	}
	return sortedStrings(out)
}

// implDependents 返回其实现检查直接依赖 id 签名的声明标识。
func (g *DepGraph) implDependents(id string) []string {
	out := make([]string, 0, len(g.implRev[id]))
	for tag := range g.implRev[id] {
		if len(tag) > 0 && tag[0] == '\x00' {
			out = append(out, implOwner(tag))
		}
	}
	return sortedStrings(out)
}

// sigSCC 在 nodes 诱导的签名依赖子图上求强连通分量（Tarjan）。
// 出边按标识排序遍历，分量内成员排序，分量按「最小成员」排序，
// 使循环组的划分与表述客观唯一。
func sigSCC(g *DepGraph, nodes []string) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	stack := []string{}
	idx := 0
	var comps [][]string
	inSet := map[string]struct{}{}
	for _, n := range nodes {
		inSet[n] = struct{}{}
	}

	var visit func(v string)
	visit = func(v string) {
		index[v] = idx
		low[v] = idx
		idx++
		stack = append(stack, v)
		onStack[v] = true

		outs := g.sigDepsOf(v)
		for _, w := range outs {
			if _, ok := inSet[w]; !ok {
				continue
			}
			if _, seen := index[w]; !seen {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}

		if low[v] == index[v] {
			comp := []string{}
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			comps = append(comps, sortedStrings(comp))
		}
	}

	for _, n := range sortedStrings(nodes) {
		if _, seen := index[n]; !seen {
			visit(n)
		}
	}

	// 分量按最小成员排序，保证输出次序确定。
	sortComps(comps)
	return comps
}
