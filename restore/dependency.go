package restore

import "sort"

// depGraph 是跨类别依赖关系核对所用的有向图：
// 边 from -> to 表示 from 的重建依赖 to 先完成。
// 键不存在于 nodes 表示悬空引用（记录根本不存在于任何备份）。
type depGraph struct {
	nodes map[RecordID]bool
	// deps[from] 为 from 依赖的全部 to，顺序按确定性规则排序。
	deps map[RecordID][]RecordID
}

// buildGraph 从快照推导领域依赖并追加 ExtraDeps。
//
// 领域推导规则：
//   - 对象实例依赖其所属对象类型定义；
//   - 链接实例依赖其两端引用的对象实例（两端相同只保留一条边）；
//   - 动作执行记录依赖其涉及的全部对象实例与链接实例（逐一核对、去重）；
//   - 指向不存在记录的依赖作为悬空引用保留，由恢复判定记为断引用。
func buildGraph(snap *Snapshot) *depGraph {
	g := &depGraph{
		nodes: map[RecordID]bool{},
		deps:  map[RecordID][]RecordID{},
	}
	addNode := func(id RecordID) { g.nodes[id] = true }
	addEdge := func(from, to RecordID) { g.deps[from] = append(g.deps[from], to) }

	for _, t := range snap.Types {
		addNode(RecordID{ClassType, t.Key})
	}
	for _, o := range snap.Objects {
		id := RecordID{ClassObject, o.Key}
		addNode(id)
		if o.TypeKey != "" {
			addEdge(id, RecordID{ClassType, o.TypeKey})
		}
	}
	for _, l := range snap.Links {
		id := RecordID{ClassLink, l.Key}
		addNode(id)
		if l.SourceObject != "" {
			addEdge(id, RecordID{ClassObject, l.SourceObject})
		}
		if l.TargetObject != "" && l.TargetObject != l.SourceObject {
			addEdge(id, RecordID{ClassObject, l.TargetObject})
		}
	}
	for _, act := range snap.Actions {
		id := RecordID{ClassAction, act.Key}
		addNode(id)
		seen := map[RecordID]bool{}
		for _, k := range act.Objects {
			ref := RecordID{ClassObject, k}
			if !seen[ref] {
				seen[ref] = true
				addEdge(id, ref)
			}
		}
		for _, k := range act.Links {
			ref := RecordID{ClassLink, k}
			if !seen[ref] {
				seen[ref] = true
				addEdge(id, ref)
			}
		}
	}
	for _, e := range snap.ExtraDeps {
		addEdge(e.From, e.To)
	}
	for from := range g.deps {
		g.deps[from] = sortRecordIDs(append([]RecordID(nil), g.deps[from]...))
	}
	return g
}

// traverseResult 是单条记录依赖链记忆化遍历的结果。
type traverseResult struct {
	ok          bool
	code        ReasonCode
	edgesLooked int
}

// resolveWithMemo 对单条记录做自底向上的记忆化恢复判定。
// memo 可跨多条记录复用；looked 非 nil 时累计实际查看的依赖边数（复杂度复核用）。
//
// 每个节点处的固定裁决顺序：
//  1. 悬空引用 -> ReasonReferenceBroken；
//  2. 种子不可用（类整体不可用/自损坏）-> 沿用种子原因；
//  3. 依赖按 (类别序, 键序) 逐一核对，首个失败原因向上传播，
//     若根源是类整体不可用则保持 ReasonClassUnavailable，
//     其余级联失败记为 ReasonDependencyUnavailable；
//  4. 全部通过 -> ReasonIntact。
func (g *depGraph) resolveWithMemo(
	id RecordID,
	dead map[RecordID]ReasonCode,
	memo map[RecordID]*traverseResult,
	looked *int,
) (*traverseResult, bool) {
	if cached, ok := memo[id]; ok {
		return cached, true
	}
	if !g.nodes[id] {
		r := &traverseResult{ok: false, code: ReasonReferenceBroken}
		memo[id] = r
		return r, false
	}
	if code, ok := dead[id]; ok {
		r := &traverseResult{ok: false, code: code}
		memo[id] = r
		return r, false
	}
	r := &traverseResult{ok: true, code: ReasonIntact}
	memo[id] = r
	for _, dep := range g.deps[id] {
		_, cached := memo[dep]
		depRes, _ := g.resolveWithMemo(dep, dead, memo, looked)
		// 每条依赖边在一次会话中只计一次工作量；命中记忆化缓存的边为 O(1) 查表。
		if looked != nil && !cached {
			*looked++
		}
		if !depRes.ok {
			r.ok = false
			r.code = ReasonDependencyUnavailable
			if depRes.code == ReasonClassUnavailable {
				r.code = ReasonClassUnavailable
			}
			break
		}
	}
	return r, false
}

// detectCycles 在给定活节点子图上检测循环依赖。
//
// 返回集合包含循环成员本身，以及依赖循环成员的全部下游节点——
// 它们的重建顺序同样无法确定。检测顺序按 (类别序, 键序)，结论确定。
func (g *depGraph) detectCycles(live map[RecordID]bool) map[RecordID]bool {
	color := map[RecordID]int{} // 0 未访问 1 在栈上 2 完成
	cyclic := map[RecordID]bool{}

	var dfs func(id RecordID) (selfCycle bool, reachesCycle bool)
	dfs = func(id RecordID) (bool, bool) {
		if color[id] == 2 {
			return false, cyclic[id]
		}
		color[id] = 1
		selfCycle := false
		downstream := false
		for _, dep := range g.deps[id] {
			if !live[dep] {
				continue
			}
			if color[dep] == 1 {
				selfCycle = true
				cyclic[dep] = true
				continue
			}
			sc, rc := dfs(dep)
			if sc {
				selfCycle = true
			}
			if rc || sc || cyclic[dep] {
				downstream = true
			}
		}
		color[id] = 2
		if selfCycle || downstream {
			cyclic[id] = true
		}
		return selfCycle, selfCycle || downstream
	}

	for _, id := range g.sortedNodes() {
		if !live[id] || color[id] != 0 {
			continue
		}
		dfs(id)
	}
	return cyclic
}

// topoPlan 在可恢复节点集合上生成唯一确定的重建计划（含类别屏障）。
//
// 类别屏障语义：barrier_c 依赖类别 c 的全部记录，而所有更高类别记录依赖
// barrier_c。这样拓扑序自动保证“任何一类数据在其依赖数据之前不会被标记
// 已重建完成”，同时使跨类别反向依赖在拓扑阶段直接暴露为无法消解的环。
func (g *depGraph) topoPlan(recoverable map[RecordID]bool) []PlanStep {
	barKey := func(c Class) string { return "barrier:" + c.String() }
	classOfBarrier := func(s string) (Class, bool) {
		for _, c := range Classes() {
			if s == barKey(c) {
				return c, true
			}
		}
		return 0, false
	}

	type tnode struct {
		id       RecordID
		isBar    bool
		barCls   Class
		out      []string
		inDegree int
	}
	nodes := map[string]*tnode{}
	keyOf := func(id RecordID) string { return id.String() }
	get := func(k string) *tnode {
		n, ok := nodes[k]
		if !ok {
			n = &tnode{}
			nodes[k] = n
		}
		return n
	}

	for id := range recoverable {
		n := get(keyOf(id))
		n.id = id
	}
	edgeSet := map[[2]string]bool{}
	addArc := func(fromK, toK string) {
		k := [2]string{fromK, toK}
		if edgeSet[k] {
			return
		}
		edgeSet[k] = true
		// 边 from -> to 表示 from 依赖 to：入度记在 from 上，
		// to 完成时沿反向邻接释放 from。
		get(fromK).inDegree++
		get(toK).out = append(nodes[toK].out, fromK)
	}
	for from := range recoverable {
		for _, to := range g.deps[from] {
			if recoverable[to] {
				addArc(keyOf(from), keyOf(to))
			}
		}
	}
	for _, c := range Classes() {
		classHasRecord := false
		for id := range recoverable {
			if id.Class == c {
				classHasRecord = true
				break
			}
		}
		if !classHasRecord {
			// 该类没有任何可恢复记录：不生成“完成”屏障，
			// 也不用它阻断更高类别（例如空对象/链接备份下的无依赖动作）。
			continue
		}
		bk := barKey(c)
		bn := get(bk)
		bn.isBar = true
		bn.barCls = c
		for id := range recoverable {
			if id.Class == c {
				addArc(bk, keyOf(id))
			} else if id.Class.Rank() > c.Rank() {
				addArc(keyOf(id), bk)
			}
		}
	}

	// 序键：屏障 (类别+1,0,"")，记录 (类别,1,记录键)。
	// 屏障恒在同类记录之后、高类记录之前；同类内按记录键打破并列。
	orderKey := func(k string) (rank int, sub int, rec string) {
		if c, ok := classOfBarrier(k); ok {
			return c.Rank() + 1, 0, ""
		}
		for _, c := range Classes() {
			prefix := c.String() + ":"
			if len(k) > len(prefix) && k[:len(prefix)] == prefix {
				return c.Rank(), 1, k[len(prefix):]
			}
		}
		return 1 << 30, 1, k
	}
	less := func(a, b string) bool {
		r1, s1, k1 := orderKey(a)
		r2, s2, k2 := orderKey(b)
		if r1 != r2 {
			return r1 < r2
		}
		if s1 != s2 {
			return s1 < s2
		}
		return k1 < k2
	}

	var ready []string
	for k, n := range nodes {
		if n.inDegree == 0 {
			ready = append(ready, k)
		}
	}
	var seq []string
	for len(ready) > 0 {
		best := 0
		for i := 1; i < len(ready); i++ {
			if less(ready[i], ready[best]) {
				best = i
			}
		}
		k := ready[best]
		ready = append(ready[:best], ready[best+1:]...)
		seq = append(seq, k)
		outs := append([]string(nil), nodes[k].out...)
		sort.Strings(outs)
		for _, to := range outs {
			nodes[to].inDegree--
			if nodes[to].inDegree == 0 {
				ready = append(ready, to)
			}
		}
	}
	// 若屏障扩展图上仍有节点未被排空（例如跨类别反向依赖使屏障无法定位），
	// 这些节点不得出现在计划中。计划只包含已获得确定位置的记录步骤。

	plan := make([]PlanStep, 0, len(seq))
	for _, k := range seq {
		n := nodes[k]
		if n.isBar {
			c := n.barCls
			plan = append(plan, PlanStep{Kind: StepBarrier, CompletedClass: &c})
			continue
		}
		id := n.id
		plan = append(plan, PlanStep{Kind: StepRecord, Record: &id})
	}
	return plan
}

// sortedNodes 确定性返回图中全部节点。
func (g *depGraph) sortedNodes() []RecordID {
	ids := make([]RecordID, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	return sortRecordIDs(ids)
}

// sortRecordIDs 按 (类别序, 键序) 排序记录标识。
func sortRecordIDs(ids []RecordID) []RecordID {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Class != ids[j].Class {
			return ids[i].Class.Rank() < ids[j].Class.Rank()
		}
		return ids[i].Key < ids[j].Key
	})
	return ids
}
