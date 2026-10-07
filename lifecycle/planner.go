package lifecycle

import "sort"

// step 是处理单元计划中的一环：一次具体迁移及其暂定变更。
type step struct {
	idx       int // 处理单元内的全局稳定序号
	target    InstanceID
	rule      *TransitionRule
	typeName  string
	req       *TransitionRequest // 根请求（级联环节也指向其根请求，携带属性/链接变更仅在根步生效）
	cascadeOf InstanceID         // 空表示该步是批次根请求
	fromState State
}

// plan 是一个根请求展开出的完整计划（含链式触发环节）。
type plan struct {
	rootIdx int
	root    *TransitionRequest
	steps   []*step
	err     *LifecycleError // 展开期错误（未声明 / 循环 / 互斥）
	loser   bool            // 根仲裁落败：最终错误取决于自身前置条件与胜出方是否成功
}

// node 是级联图中的一个实例节点（一个处理单元内每实例至多一个节点）。
type node struct {
	id    InstanceID
	typ   *ObjectType
	step  *step // 胜出的步（仲裁后唯一）
	out   []*edge
	cycle bool
	color uint8 // 0=white,1=gray,2=black
}

type edge struct {
	via    LinkType
	toRule string
	to     *node
	// claimConflict 表示该级联边指向一个被直接根请求显式迁移的节点。
	// 该边被丢弃（邻居不由本链强制迁移），不构成互斥拒绝。
	claimConflict bool
}

// buildResult 持有本处理单元的计划图。
type buildResult struct {
	plans    []*plan
	nodes    map[InstanceID]*node
	allSteps []*step
	touched  []InstanceID // 涉及的全部实例（含邻居），用于加锁
}

// planner 把一批根请求展开为计划：解析规则、展开级联、
// 在任何一环生效之前检测循环、按调用方优先顺序仲裁互斥迁移。
type planner struct {
	store *Store
}

func newPlanner(s *Store) *planner { return &planner{store: s} }

// Build 在持有 Store RLock 的前提下构建计划。本函数不做任何副作用修改。
func (p *planner) Build(reqs []TransitionRequest) *buildResult {
	res := &buildResult{nodes: map[InstanceID]*node{}}
	touched := map[InstanceID]struct{}{}

	// 第一阶段：为每个根请求创建步与节点，解析规则与当前状态。
	for i := range reqs {
		req := &reqs[i]
		pl := &plan{rootIdx: i, root: req}
		res.plans = append(res.plans, pl)
		touched[req.Instance] = struct{}{}

		inst, ok := p.store.instances[req.Instance]
		if !ok {
			pl.err = newErr(ErrUndeclared, req.Instance, req.Rule, "instance does not exist")
			continue
		}
		t := p.store.types[inst.Type]
		if t == nil {
			pl.err = newErr(ErrUndeclared, req.Instance, req.Rule, "object type not registered")
			continue
		}
		rule, ok := t.Transitions[req.Rule]
		if !ok {
			pl.err = newErr(ErrUndeclared, req.Instance, req.Rule, "transition rule not declared")
			continue
		}
		fromOK := false
		for _, from := range rule.From {
			if from == inst.State {
				fromOK = true
				break
			}
		}
		if !fromOK {
			pl.err = newErr(ErrUndeclared, req.Instance, req.Rule,
				"transition not allowed from current state "+string(inst.State))
			continue
		}

		st := &step{
			idx:       len(res.allSteps),
			target:    req.Instance,
			rule:      rule,
			typeName:  inst.Type,
			req:       req,
			fromState: inst.State,
		}
		res.allSteps = append(res.allSteps, st)
		pl.steps = append(pl.steps, st)

		n := res.nodes[req.Instance]
		if n == nil {
			n = &node{id: req.Instance, typ: t}
			res.nodes[req.Instance] = n
		}
		n.step = arbitrate(n.step, st)
	}

	// 第二阶段：按胜出步分层展开级联边。每一轮都基于当前的胜出步集合
	// 创建新一层节点，然后重新仲裁；重复直到不再产生新节点，保证边始终
	// 跟随最终胜出者。邻居来自当前链接结构（引擎在锁内会整体重建）。
	p.expandLevels(res, touched)

	// 第三阶段：在任何一环生效之前做循环检测（三色 DFS）。
	ids := make([]InstanceID, 0, len(res.nodes))
	for id := range res.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		p.dfsCycle(res.nodes[id], res)
	}

	// 第四阶段：把每个节点的胜出步收集回各 plan，并写入展开期错误。
	p.collect(res)

	for id := range touched {
		res.touched = append(res.touched, id)
	}
	res.touched = uniqueSortedIDs(res.touched)
	return res
}

// expandLevels 分层展开级联：每轮只处理上一轮确定的节点，按其胜出步
// 的 Cascades 创建邻居节点；循环往复直到闭包稳定。
func (p *planner) expandLevels(res *buildResult, touched map[InstanceID]struct{}) {
	frontier := make([]*node, 0, len(res.nodes))
	for _, n := range res.nodes {
		frontier = append(frontier, n)
	}
	sort.Slice(frontier, func(i, j int) bool { return frontier[i].id < frontier[j].id })
	for len(frontier) > 0 {
		next := map[InstanceID]*node{}
		for _, n := range frontier {
			if n.step == nil || n.typ == nil {
				continue
			}
			n.out = n.out[:0]
			for _, cas := range n.step.rule.Cascades {
				for _, nb := range p.store.neighborsLocked(n.id, cas.Link) {
					touched[nb] = struct{}{}
					nbInst, ok := p.store.instances[nb]
					if !ok {
						continue
					}
					nbType := p.store.types[nbInst.Type]
					nbNode := res.nodes[nb]
					if nbNode == nil {
						nbNode = &node{id: nb, typ: nbType}
						res.nodes[nb] = nbNode
					}
					var nbRule *TransitionRule
					if nbType != nil {
						nbRule = nbType.Transitions[cas.ToRule]
					}
					cs := &step{
						idx:       len(res.allSteps),
						target:    nb,
						rule:      nbRule,
						typeName:  nbInst.Type,
						req:       n.step.req,
						cascadeOf: n.id,
						fromState: nbInst.State,
					}
					res.allSteps = append(res.allSteps, cs)
					prev := nbNode.step
					// 已被直接根占用的邻居：直接根胜出，级联边被丢弃；
					// 已被另一条级联占用：保留先到者，共享目标不换归属。
					if prev == nil {
						nbNode.step = cs
					}
					ed := &edge{via: cas.Link, toRule: cas.ToRule, to: nbNode}
					// 目标节点的胜出步属于别的直接根：汇聚冲突。仅当该直接
					// 根的优先级严格更高（或同优先级且声明更靠前）时，级联
					// 发起根才落败；否则级联步胜出，直接根落败。
					// 汇聚冲突仅在“直接根”胜出时成立：直接根（非级联步）
					// 优先于级联衍生步；级联发起根因此落败。
					directWins := nbNode.step.cascadeOf == "" && nbNode.step.req != n.step.req
					if directWins {
						ed.claimConflict = true
					}
					n.out = append(n.out, ed)
					if prev == nil || prev != nbNode.step {
						next[nb] = nbNode
					}
				}
			}
		}
		frontier = frontier[:0]
		fn := make([]*node, 0, len(next))
		for _, n := range next {
			fn = append(fn, n)
		}
		sort.Slice(fn, func(i, j int) bool { return fn[i].id < fn[j].id })
		frontier = append(frontier, fn...)
	}
}

// dfsCycle 用三色深度优先搜索标记回边涉及的节点。
func (p *planner) dfsCycle(n *node, res *buildResult) {
	if n.color != 0 {
		return
	}
	n.color = 1
	for _, e := range n.out {
		if e.to.color == 1 {
			n.cycle = true
			e.to.cycle = true
		} else if e.to.color == 0 {
			p.dfsCycle(e.to, res)
			if e.to.cycle {
				n.cycle = true
			}
		}
	}
	n.color = 2
}

// collect 把节点映射回计划，并确定展开期错误：
// 未声明（缺失级联规则）、循环、互斥落败。
func (p *planner) collect(res *buildResult) {
	// 重新按根追溯每个 plan 覆盖的步骤集合（沿胜出图）。
	for _, pl := range res.plans {
		if pl.err != nil {
			continue
		}
		root := pl.root
		n := res.nodes[root.Instance]
		if n == nil || n.step == nil {
			continue
		}
		// 根仲裁：该实例的胜出步若属于别的根请求，本根标记为落败候选。
		// 最终错误在评估阶段决定：自身前置条件不成立 -> ErrPrecondition；
		// 否则若胜出方成功 -> ErrMutex；胜出方也失败则跟随失败原因。
		if n.step.req != pl.root {
			pl.loser = true
			continue
		}
		visited := map[InstanceID]bool{}
		var walk func(*node) *LifecycleError
		walk = func(cur *node) *LifecycleError {
			if visited[cur.id] {
				return newErr(ErrCycle, cur.id, "", "cascade chain revisits instance")
			}
			visited[cur.id] = true
			if cur.cycle {
				return newErr(ErrCycle, cur.id, stepRuleName(cur.step), "cascade chain contains a cycle")
			}
			if cur.step.rule == nil {
				return newErr(ErrUndeclared, cur.id, "", "cascade transition rule not declared on target type")
			}
			pl.steps = append(pl.steps, cur.step)
			for _, e := range cur.out {
				if e.claimConflict {
					// 邻居被直接根显式迁移：本链不强制迁移它，也不拒绝本根。
					continue
				}
				if err := walk(e.to); err != nil {
					return err
				}
			}
			return nil
		}
		pl.err = walk(n)
		// 根步在第一阶段已加入；去重。
		pl.steps = dedupSteps(pl.steps)
	}
}

// arbitrate 按调用方声明的优先顺序决定同实例上的胜出步：
// Priority 数值小者优先；平手时处理单元内序号小（声明顺序靠前）者胜。
// 只有声明了同一非空互斥组的两条迁移才是“互斥迁移”；其它汇聚冲突
// 同样以 ErrMutex 拒绝败者（该实例一个处理单元只能推进一次）。
func arbitrate(a, b *step) *step {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	// 直接根请求优先于级联衍生步：一个被显式触发的迁移不应被别的链
	// 覆盖（级联汇聚到已显式请求的实例时，显式请求胜出）。
	aRoot, bRoot := a.cascadeOf == "", b.cascadeOf == ""
	if aRoot != bRoot {
		if aRoot {
			return a
		}
		return b
	}
	mutexPair := a.rule != nil && b.rule != nil &&
		a.rule.MutexGroup != "" && a.rule.MutexGroup == b.rule.MutexGroup
	_ = mutexPair
	if b.req.Priority < a.req.Priority {
		return b
	}
	if b.req.Priority == a.req.Priority && b.idx < a.idx {
		return b
	}
	return a
}

func stepRuleName(st *step) string {
	if st == nil || st.rule == nil {
		return ""
	}
	return st.rule.Name
}

func dedupSteps(in []*step) []*step {
	seen := map[InstanceID]bool{}
	out := make([]*step, 0, len(in))
	for _, st := range in {
		if seen[st.target] {
			continue
		}
		seen[st.target] = true
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].idx < out[j].idx })
	return out
}
