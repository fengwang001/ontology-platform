package ontology

import (
	"sort"
	"sync"
)

// Logger 记录每次调用的输入、最终输出与传播路径依据。
type Logger interface {
	LogCall(action ActionDeclaration, report Report, stateBefore WorldState)
}

// Engine 在单个互斥锁内串行执行动作，因此对外呈现可串行化的并发语义；
// 等价的串行顺序即锁的获取顺序，被拒绝的动作在该顺序中不产生任何影响。
type Engine struct {
	mu      sync.Mutex
	store   Store
	decider Decider
	logger  Logger
}

// CheckCounters 以不依赖实现细节的可观测方式统计单次动作的权限检查次数。
// 可见性与授权检查次数都只与该动作实际声明/实际触及的实例数量相关
// （规模无关性由 TestCheckCount* 证明）。
type CheckCounters struct {
	VisibilityChecks    int
	AuthorizationChecks int
}

type countedDecider struct {
	Decider
	c *CheckCounters
}

func (d countedDecider) Visible(s SubjectID, in Instance, depth int) bool {
	d.c.VisibilityChecks++
	return d.Decider.Visible(s, in, depth)
}

func (d countedDecider) Allowed(s SubjectID, in Instance, op Operation, depth int) (bool, bool) {
	d.c.AuthorizationChecks++
	return d.Decider.Allowed(s, in, op, depth)
}

// NewEngine 构造引擎。
func NewEngine(st Store, d Decider, lg Logger) *Engine {
	return &Engine{store: st, decider: d, logger: lg}
}

// Execute 以全有或全无方式执行一次动作。
//
// 多层级授权模型：传播路径上每个被触及实例恰好贡献"一个层级"的票——
//   - 层级 0：直接目标实例，检查其直接操作；
//   - 层级 k>=1：经第 k 条链接首次到达的实例，检查在该实例上施加的
//     级联操作。
//
// 环去重保证每个实例只出现一次、只检查一次。所有层级的票（允许/拒绝/
// 弃权）按动作声明的 MergePolicy 归约：MergeAll 要求所有非弃权票允许，
// MergeAny 要求存在一张允许票；归约只依赖票的多重集，与访问顺序无关。
func (e *Engine) Execute(action ActionDeclaration) (Report, *ActionError) {
	e.mu.Lock()
	defer e.mu.Unlock()

	before := e.store.Snapshot()
	rep := Report{Action: action.Name, AuditIndex: -1}
	counters := &CheckCounters{}
	dec := countedDecider{Decider: e.decider, c: counters}
	fail := func(kind ErrorKind, msg string) (Report, *ActionError) {
		rep.Reason = msg
		rep.Checks = counters.VisibilityChecks + counters.AuthorizationChecks
		e.log(action, rep, before)
		return rep, &ActionError{Kind: kind, Msg: msg}
	}

	// 优先级 1：声明合法性。
	if err := validate(action); err != nil {
		return fail(RejectInvalidDeclaration, err.Error())
	}

	roots := make([]InstanceID, len(action.Direct))
	for i, op := range action.Direct {
		roots[i] = op.Target
	}

	// 可达性传播（环去重、NoPropagate、深度上限）。规则按链接类型排序，
	// 使首次到达深度只由图与声明决定，不依赖规则书写顺序。
	ordered := action
	ordered.Cascades = orderedRules(action.Cascades)
	visibility := func(inst Instance, depth int) bool {
		return dec.Visible(action.Subject, inst, depth)
	}
	reached, exceeded := traverse(e.store, ordered, roots, visibility)

	// 优先级 2：传播深度超过声明上限。
	if exceeded {
		return fail(RejectDepthExceeded, "cascade propagation depth exceeds declared limit")
	}

	// 优先级 3：直接目标可见性。OpCreate 的目标尚不存在，跳过可见性检查。
	for _, op := range action.Direct {
		if op.Op == OpCreate {
			continue
		}
		inst, ok := e.store.Get(op.Target)
		if !ok || !dec.Visible(action.Subject, inst, 0) {
			return fail(RejectDirectInvisible, "direct target instance is not visible to subject")
		}
	}

	// 级联实例可见性已在传播中分类（n.invisible）；按互斥模式处理。
	var cascadeEntries []TraversalEntry
	var skipped []SkippedInstance
	cascadeInvisible := false
	directEntries := make([]TraversalEntry, 0, len(action.Direct))
	for _, op := range action.Direct {
		t := op.Type
		if in, ok := e.store.Get(op.Target); ok {
			t = in.Type
		}
		directEntries = append(directEntries, TraversalEntry{
			Instance: op.Target, Type: t, Depth: 0, Verdict: "pending",
		})
	}
	for idx, n := range reached {
		if n.depth == 0 {
			continue
		}
		if n.invisible {
			if action.InvisibleMode == RejectOnInvisible {
				cascadeInvisible = true
				cascadeEntries = append(cascadeEntries, TraversalEntry{
					Instance: n.id, Depth: n.depth, LinkType: n.via, Verdict: "invisible",
				})
				continue
			}
			skipped = append(skipped, SkippedInstance{
				Instance:        n.id,
				Depth:           n.depth,
				WithheldEffects: append([]Operation(nil), n.operations...),
				PrunedSubtree:   prunedCount(idx, reached),
			})
			cascadeEntries = append(cascadeEntries, TraversalEntry{
				Instance: n.id, Depth: n.depth, LinkType: n.via, Verdict: "skipped",
			})
			continue
		}
		inst, ok := e.store.Get(n.id)
		if !ok {
			continue
		}
		cascadeEntries = append(cascadeEntries, TraversalEntry{
			Instance: n.id, Type: inst.Type, Depth: n.depth, LinkType: n.via,
		})
	}

	// 存活集合：深度 0 根 + 所有未被标记不可见的级联实例。
	alive := map[InstanceID]bool{}
	for _, n := range reached {
		if n.depth == 0 {
			alive[n.id] = true
		}
	}
	for _, n := range reached {
		if n.depth > 0 && !n.invisible {
			alive[n.id] = true
		}
	}

	// 优先级 4：级联不可见且要求整体拒绝。
	if cascadeInvisible {
		rep.Path = append(directEntries, cascadeEntries...)
		return fail(RejectCascadeInvisible, "a cascaded instance is not visible and reject-on-invisible is set")
	}

	// 逐层授权：每个被触及实例贡献一张票。
	var votes []LayerDecision
	for i, op := range action.Direct {
		inst := Instance{ID: op.Target, Type: op.Type, Attrs: op.NewAttrs}
		if op.Op != OpCreate {
			inst, _ = e.store.Get(op.Target)
		}
		vote := voteOn(dec, action.Subject, inst, []Operation{op.Op}, 0)
		votes = append(votes, vote)
		verdict := "permit"
		if vote.Abstain || !vote.Allowed {
			verdict = "deny"
		}
		directEntries[i] = TraversalEntry{
			Instance: op.Target, Type: inst.Type, Depth: 0,
			Verdict: verdict, Decisions: []LayerDecision{vote},
		}
	}

	for i := range cascadeEntries {
		entry := &cascadeEntries[i]
		if entry.Verdict == "skipped" {
			continue
		}
		inst, _ := e.store.Get(entry.Instance)
		node := reached[reachedIdx(reached, entry.Instance)]
		vote := voteOn(dec, action.Subject, inst, node.operations, node.depth)
		votes = append(votes, vote)
		entry.Decisions = []LayerDecision{vote}
		if vote.Abstain || !vote.Allowed {
			entry.Verdict = "deny"
		} else {
			entry.Verdict = "permit"
		}
	}

	rep.Path = append(directEntries, cascadeEntries...)
	rep.Skipped = skipped

	// 最终合并（顺序无关）：最终放行与否完全由合并规则决定。
	if !mergeDecisions(action.Merge, votes) {
		return fail(RejectDenied, "authorization denied by merge policy")
	}

	// 在暂存区内施加全部变更；此前任何失败都未触碰暂存区与时钟。
	e.store.Begin()
	applied := applyDirect(e.store, action.Direct)
	for _, n := range reached {
		if n.depth == 0 || !alive[n.id] {
			continue
		}
		inst, ok := e.store.Get(n.id)
		if !ok {
			continue // 已被本事务删除：后续影响折叠为空，不复活实例
		}
		// 同一实例上多种级联影响的固定顺序：先更新后删除，
		// 删除若存在则最终胜出；不依赖规则声明或边的枚举顺序。
		for _, op := range orderedOps(n.operations) {
			applyCascade(e.store, inst, op)
			applied = append(applied, CascadeEffect{Target: n.id, Type: inst.Type, Op: op, Depth: n.depth})
		}
	}
	e.store.commit()

	seq := e.store.Clock()
	e.store.AppendAudit(AuditRecord{
		Seq: seq, Action: action.Name, Subject: action.Subject,
		Direct:  append([]DirectOp(nil), action.Direct...),
		Applied: applied, Skipped: skipped,
		Checks: counters.VisibilityChecks + counters.AuthorizationChecks,
	})
	rep.Committed = true
	rep.Checks = counters.VisibilityChecks + counters.AuthorizationChecks
	rep.AuditIndex = seq
	e.log(action, rep, before)
	return rep, nil
}

func (e *Engine) log(action ActionDeclaration, rep Report, before WorldState) {
	if e.logger != nil {
		e.logger.LogCall(action, rep, before)
	}
}

// voteOn 在单个层级 depth 上，对实例 inst 施加的一组操作做授权归约：
// 该层所有"非弃权"操作都允许 => 该层允许；有任一拒绝 => 该层拒绝；
// 全部弃权 => 该层弃权。
func voteOn(d Decider, s SubjectID, inst Instance, ops []Operation, depth int) LayerDecision {
	allowed, voted := true, false
	for _, op := range ops {
		ok, abstain := d.Allowed(s, inst, op, depth)
		if abstain {
			continue
		}
		voted = true
		if !ok {
			allowed = false
		}
	}
	return LayerDecision{Depth: depth, Allowed: allowed, Abstain: !voted}
}

func reachedIdx(reached []reachedNode, id InstanceID) int {
	for i := range reached {
		if reached[i].id == id {
			return i
		}
	}
	return -1
}

// prunedCount 统计 BFS 父子树中以 root 为根的子树大小（含 root）。
// 由于遍历不从不可见节点继续扩展，该子树正好等于被整体剪除、无法经任何
// 其它全可见路径到达的实例集合；不同跳过根的子树互不重叠。
func prunedCount(root int, reached []reachedNode) int {
	count := 0
	var walk func(int)
	walk = func(i int) {
		count++
		for j := i + 1; j < len(reached); j++ {
			if reached[j].parent == i {
				walk(j)
			}
		}
	}
	walk(root)
	return count
}

func applyDirect(st Store, ops []DirectOp) []CascadeEffect {
	applied := make([]CascadeEffect, 0, len(ops))
	for _, op := range ops {
		switch op.Op {
		case OpCreate:
			st.stageCreate(op)
		case OpUpdate:
			st.stageUpdate(op.Target, op.NewAttrs)
		case OpDelete:
			st.stageDelete(op.Target)
		}
		applied = append(applied, CascadeEffect{
			Target: op.Target, Type: op.Type, Op: op.Op, Depth: 0,
			Attrs: attrsForOp(op),
		})
	}
	return applied
}

func attrsForOp(op DirectOp) map[string]string {
	if op.Op == OpUpdate || op.Op == OpCreate {
		return cloneAttrs(op.NewAttrs)
	}
	return nil
}

func applyCascade(st Store, inst Instance, op Operation) {
	switch op {
	case OpUpdate:
		st.stageUpdate(inst.ID, map[string]string{"touched": "1"})
	case OpDelete:
		st.stageDelete(inst.ID)
	}
}

func sortedOps(ops []Operation) []Operation {
	out := append([]Operation(nil), ops...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// orderedOps 固定同一实例上多种级联影响的生效顺序：read（无副作用）、
// update、delete。这样跨对象类型/规则的并发合并不依赖声明顺序。
func orderedOps(ops []Operation) []Operation {
	set := map[Operation]bool{}
	for _, op := range ops {
		set[op] = true
	}
	var out []Operation
	for _, op := range []Operation{OpRead, OpUpdate, OpDelete} {
		if set[op] {
			out = append(out, op)
		}
	}
	return out
}

// orderedRules 返回按链接类型（再按方向）排序的规则副本，
// 保证 BFS 首次到达深度在不同声明书写顺序下保持确定。
func orderedRules(rules []CascadeRule) []CascadeRule {
	out := append([]CascadeRule(nil), rules...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LinkType != out[j].LinkType {
			return out[i].LinkType < out[j].LinkType
		}
		if out[i].Outgoing != out[j].Outgoing {
			return !out[i].Outgoing
		}
		return out[i].Effect < out[j].Effect
	})
	return out
}
