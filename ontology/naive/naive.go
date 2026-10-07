// Package naive 提供与生产实现完全独立维护的朴素参照实现。
//
// 它刻意不使用生产代码的遍历器、合并器或暂存存储，而是：
//   - 用邻接表 map 与递归路径枚举（带 visited 去环）；
//   - 用最直白的循环重新实现层级合并；
//   - 在一份深拷贝世界上直接改，失败即丢弃整个拷贝。
//
// 差分测试在大量随机图与动作序列上逐项对照两侧的裁决与终态。
package naive

import (
	"sort"

	ont "ontology/ontology"
)

// Policy 是参照侧的授权策略：显式不可见集合 + 按深度/操作的允许集合。
// 调用方把策略快照传入，它不实现生产侧的 ont.Decider 接口，
// 以保证两侧实现彼此独立。
type Policy struct {
	Invisible map[ont.InstanceID]bool
	// Allow[instance][op][depth] 表示该层级显式放行；缺失表示弃权。
	Allow map[ont.InstanceID]map[ont.Operation]map[int]bool
}

// World 是参照实现自有的关系图世界。
type World struct {
	inst  map[ont.InstanceID]ont.Instance
	edges []ontEdge
	clock int64
	audit []ont.AuditRecord
	pol   map[ont.SubjectID]Policy
}

type ontEdge struct {
	link ont.LinkTypeID
	from ont.InstanceID
	to   ont.InstanceID
}

// Outcome 是参照实现对一次动作的裁决与执行结果。
type Outcome struct {
	Committed bool
	Reason    ont.ErrorKind
	Skipped   []ont.SkippedInstance
	Applied   []ont.CascadeEffect
	State     ont.WorldState
}

// NewWorld 从生产状态快照与按主体的策略表构造参照世界（深拷贝）。
func NewWorld(state ont.WorldState, policies map[ont.SubjectID]Policy) *World {
	w := &World{
		inst:  map[ont.InstanceID]ont.Instance{},
		clock: state.Clock,
		audit: append([]ont.AuditRecord(nil), state.Audit...),
		pol:   policies,
	}
	for id, in := range state.Instances {
		in.Attrs = cloneMap(in.Attrs)
		w.inst[id] = in
	}
	for _, e := range state.Edges {
		w.edges = append(w.edges, ontEdge{link: e.LinkType, from: e.From, to: e.To})
	}
	return w
}

// Run 以最直白的方式模拟一次动作，返回裁决与执行后的世界快照。
func (w *World) Run(a ont.ActionDeclaration) Outcome {
	fail := func(k ont.ErrorKind) Outcome {
		return Outcome{Reason: k, State: w.snapshot()}
	}
	if !validAction(a) {
		return fail(ont.RejectInvalidDeclaration)
	}
	pol := w.pol[a.Subject]
	rules := append([]ont.CascadeRule(nil), a.Cascades...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].LinkType != rules[j].LinkType {
			return rules[i].LinkType < rules[j].LinkType
		}
		if rules[i].Outgoing != rules[j].Outgoing {
			return !rules[i].Outgoing
		}
		return rules[i].Effect < rules[j].Effect
	})

	type hit struct {
		id     ont.InstanceID
		depth  int
		via    ont.LinkTypeID
		ops    map[ont.Operation]bool
		parent ont.InstanceID
		stop   bool
		hidden bool
	}
	hits := map[ont.InstanceID]*hit{}
	var order []ont.InstanceID
	for _, op := range a.Direct {
		if _, ok := hits[op.Target]; !ok {
			hits[op.Target] = &hit{id: op.Target, depth: 0, ops: map[ont.Operation]bool{}}
			order = append(order, op.Target)
		}
	}
	// 深度越界是纯图结构事实，先于可见性判定：做一次与可见性无关的
	// 最短深度 BFS，发现 >MaxDepth 的可传播可达实例即标记越界。
	shortest := map[ont.InstanceID]int{}
	stopMark := map[ont.InstanceID]bool{}
	var dq []ont.InstanceID
	for _, r := range uniqueRoots(a) {
		if _, ok := shortest[r]; !ok {
			shortest[r] = 0
			dq = append(dq, r)
		}
	}
	depthExceeded := false
	for dh := 0; dh < len(dq); dh++ {
		cur := dq[dh]
		if stopMark[cur] {
			continue
		}
		for _, rule := range rules {
			for _, nb := range w.neighborsForDepth(cur, rule) {
				if _, seen := shortest[nb]; seen {
					continue
				}
				nd := shortest[cur] + 1
				shortest[nb] = nd
				if rule.NoPropagate {
					stopMark[nb] = true
				}
				if nd > a.MaxDepth && !rule.NoPropagate {
					depthExceeded = true
				}
				dq = append(dq, nb)
			}
		}
	}
	queue := append([]ont.InstanceID(nil), uniqueRoots(a)...)
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		curHit := hits[cur]
		if curHit.stop || curHit.hidden {
			continue
		}
		for _, rule := range rules {
			for _, nb := range w.neighbors(cur, rule) {
				nd := curHit.depth + 1
				if h, seen := hits[nb]; seen {
					h.ops[rule.Effect] = true
					continue
				}
				if nd > a.MaxDepth && !rule.NoPropagate {
					depthExceeded = true
					continue
				}
				nbInst, exists := w.inst[nb]
				hidden := !exists || pol.Invisible[nb]
				_ = nbInst
				h := &hit{
					id: nb, depth: nd, via: rule.LinkType, parent: cur,
					ops:    map[ont.Operation]bool{rule.Effect: true},
					stop:   rule.NoPropagate,
					hidden: hidden,
				}
				hits[nb] = h
				order = append(order, nb)
				queue = append(queue, nb)
			}
		}
	}
	if depthExceeded {
		return fail(ont.RejectDepthExceeded)
	}

	for _, op := range a.Direct {
		if op.Op == ont.OpCreate {
			continue
		}
		if _, ok := w.inst[op.Target]; !ok || pol.Invisible[op.Target] {
			return fail(ont.RejectDirectInvisible)
		}
	}

	skippedRoots := map[ont.InstanceID]bool{}
	var skipped []ont.SkippedInstance
	skippedOrder := []ont.InstanceID{}
	for _, id := range order {
		h := hits[id]
		if h.depth == 0 {
			continue
		}
		if h.hidden {
			if a.InvisibleMode == ont.RejectOnInvisible {
				return fail(ont.RejectCascadeInvisible)
			}
			skippedRoots[id] = true
			skippedOrder = append(skippedOrder, id)
			skipped = append(skipped, ont.SkippedInstance{
				Instance: id, Depth: h.depth, WithheldEffects: sortedOps(h.ops),
			})
		}
	}

	// 遍历已不从隐藏节点扩展，故每个跳过根的剪除子树可由父子树精确计数。
	for i := range skipped {
		rootID := skippedOrder[i]
		n := 0
		var walk func(ont.InstanceID)
		walk = func(x ont.InstanceID) {
			n++
			for _, id := range order {
				if hits[id].parent == x {
					walk(id)
				}
			}
		}
		walk(rootID)
		skipped[i].PrunedSubtree = n
	}
	blockedBy := skippedRoots

	// 每个被触及实例贡献"一个层级"的票：深度 0 为直接操作，
	// 深度 k>=1 为在该实例上施加的级联操作。全部票按合并策略归约。
	anyVoted := false
	anyAllow := false
	allAllow := true
	register := func(id ont.InstanceID, ops []ont.Operation, depth int) {
		allow, voted := true, false
		for _, op := range ops {
			m := lookupMap(pol.Allow, id, op)
			if m == nil {
				continue
			}
			v, has := m[depth]
			if !has {
				continue
			}
			voted = true
			if !v {
				allow = false
			}
		}
		if !voted {
			return
		}
		anyVoted = true
		if allow {
			anyAllow = true
		} else {
			allAllow = false
		}
	}
	for _, op := range a.Direct {
		register(op.Target, []ont.Operation{op.Op}, 0)
	}
	for _, id := range order {
		h := hits[id]
		if h.depth == 0 {
			continue
		}
		if _, blocked := blockedBy[id]; blocked {
			continue
		}
		register(id, sortedOps(h.ops), h.depth)
	}
	if !anyVoted {
		return fail(ont.RejectDenied)
	}
	if a.Merge == ont.MergeAny {
		if !anyAllow {
			return fail(ont.RejectDenied)
		}
	} else {
		if !allAllow {
			return fail(ont.RejectDenied)
		}
	}

	next := w.copy()
	var applied []ont.CascadeEffect
	for _, op := range a.Direct {
		switch op.Op {
		case ont.OpCreate:
			next.inst[op.Target] = ont.Instance{
				ID: op.Target, Type: op.Type, Version: 1, Attrs: cloneMap(op.NewAttrs),
			}
		case ont.OpUpdate:
			in := next.inst[op.Target]
			in.Attrs = mergeAttrs(in.Attrs, op.NewAttrs)
			in.Version++
			next.inst[op.Target] = in
		case ont.OpDelete:
			delete(next.inst, op.Target)
		}
		var attrs map[string]string
		if op.Op == ont.OpUpdate || op.Op == ont.OpCreate {
			attrs = cloneMap(op.NewAttrs)
		}
		applied = append(applied, ont.CascadeEffect{
			Target: op.Target, Type: op.Type, Op: op.Op, Attrs: attrs,
		})
	}
	for _, id := range order {
		h := hits[id]
		if h.depth == 0 {
			continue
		}
		if _, blocked := blockedBy[id]; blocked {
			continue
		}
		in, ok := next.inst[id]
		if !ok {
			continue
		}
		for _, op := range orderedOps(h.ops) {
			switch op {
			case ont.OpUpdate:
				in.Attrs = mergeAttrs(in.Attrs, map[string]string{"touched": "1"})
				in.Version++
				next.inst[id] = in
			case ont.OpDelete:
				delete(next.inst, id)
			}
			applied = append(applied, ont.CascadeEffect{
				Target: id, Type: in.Type, Op: op, Depth: h.depth,
			})
		}
	}
	next.clock++
	next.audit = append(next.audit, ont.AuditRecord{
		Seq: next.clock, Action: a.Name, Subject: a.Subject,
		Direct:  append([]ont.DirectOp(nil), a.Direct...),
		Applied: applied, Skipped: skipped,
	})
	w.inst, w.edges, w.clock, w.audit = next.inst, next.edges, next.clock, next.audit
	return Outcome{Committed: true, Skipped: skipped, Applied: applied, State: w.snapshot()}
}

func lookupMap(m map[ont.InstanceID]map[ont.Operation]map[int]bool,
	id ont.InstanceID, op ont.Operation) map[int]bool {
	if byOp, ok := m[id]; ok {
		return byOp[op]
	}
	return nil
}

func (w *World) neighbors(id ont.InstanceID, rule ont.CascadeRule) []ont.InstanceID {
	set := map[ont.InstanceID]bool{}
	for _, e := range w.edges {
		if e.link != rule.LinkType {
			continue
		}
		if rule.Outgoing && e.from == id {
			set[e.to] = true
		}
		if !rule.Outgoing && e.to == id {
			set[e.from] = true
		}
	}
	out := make([]ont.InstanceID, 0, len(set))
	for id := range set {
		if _, alive := w.inst[id]; !alive {
			continue // 悬空链接目标不参与传播
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// neighborsForDepth 与 neighbors 相同；深度预扫描概念上独立于主体可见性，
// 在本内存参照中已删除实例本就不出现在 w.inst，故直接复用。
func (w *World) neighborsForDepth(id ont.InstanceID, rule ont.CascadeRule) []ont.InstanceID {
	return w.neighbors(id, rule)
}

func (w *World) copy() *World {
	c := &World{inst: map[ont.InstanceID]ont.Instance{}, pol: w.pol, clock: w.clock}
	for id, in := range w.inst {
		in.Attrs = cloneMap(in.Attrs)
		c.inst[id] = in
	}
	c.edges = append([]ontEdge(nil), w.edges...)
	c.audit = append([]ont.AuditRecord(nil), w.audit...)
	return c
}

func (w *World) snapshot() ont.WorldState {
	insts := map[ont.InstanceID]ont.Instance{}
	for id, in := range w.inst {
		in.Attrs = cloneMap(in.Attrs)
		insts[id] = in
	}
	edges := make([]ont.Edge, 0, len(w.edges))
	for _, e := range w.edges {
		edges = append(edges, ont.Edge{LinkType: e.link, From: e.from, To: e.to})
	}
	return ont.WorldState{
		Instances: insts, Edges: edges, Clock: w.clock,
		Audit: append([]ont.AuditRecord(nil), w.audit...),
	}
}

// State 返回参照世界当前的可观察状态。
func (w *World) State() ont.WorldState { return w.snapshot() }

func uniqueRoots(a ont.ActionDeclaration) []ont.InstanceID {
	seen := map[ont.InstanceID]bool{}
	var out []ont.InstanceID
	for _, op := range a.Direct {
		if !seen[op.Target] {
			seen[op.Target] = true
			out = append(out, op.Target)
		}
	}
	return out
}

func sortedOps(set map[ont.Operation]bool) []ont.Operation {
	var out []ont.Operation
	for op := range set {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// orderedOps 固定同一实例上多种级联影响顺序：read、update、delete。
func orderedOps(set map[ont.Operation]bool) []ont.Operation {
	var out []ont.Operation
	for _, op := range []ont.Operation{ont.OpRead, ont.OpUpdate, ont.OpDelete} {
		if set[op] {
			out = append(out, op)
		}
	}
	return out
}

func cloneMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mergeAttrs(base, patch map[string]string) map[string]string {
	out := cloneMap(base)
	for k, v := range patch {
		out[k] = v
	}
	return out
}

func validAction(a ont.ActionDeclaration) bool {
	if a.Name == "" || a.Subject == "" || len(a.Direct) == 0 || a.MaxDepth < 0 {
		return false
	}
	if a.InvisibleMode != ont.RejectOnInvisible && a.InvisibleMode != ont.SkipOnInvisible {
		return false
	}
	if a.Merge != ont.MergeAll && a.Merge != ont.MergeAny {
		return false
	}
	seen := map[string]bool{}
	for _, op := range a.Direct {
		if op.Target == "" || op.Type == "" {
			return false
		}
		switch op.Op {
		case ont.OpCreate, ont.OpRead, ont.OpUpdate, ont.OpDelete:
		default:
			return false
		}
		k := string(op.Op) + ":" + string(op.Target)
		if seen[k] {
			return false
		}
		seen[k] = true
	}
	seenLink := map[ont.LinkTypeID]bool{}
	for _, r := range a.Cascades {
		if r.LinkType == "" {
			return false
		}
		switch r.Effect {
		case ont.OpRead, ont.OpUpdate, ont.OpDelete:
		default:
			return false
		}
		if seenLink[r.LinkType] {
			return false
		}
		seenLink[r.LinkType] = true
	}
	return true
}
