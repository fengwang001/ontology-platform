package naive

import "sort"

// Execute 在副本上串行模拟一个批次：整批成功才提交副本，任一环节失败
// 则丢弃副本（天然整体撤销）。
func (m *Model) Execute(reqs ...Request) []Outcome {
	m.mu.Lock()
	defer m.mu.Unlock()

	outs := make([]Outcome, len(reqs))
	for i, r := range reqs {
		outs[i] = Outcome{Instance: r.Instance, Rule: r.Rule}
	}
	ci, cl := m.snapshot()

	setErr := func(i, code int, detail string) {
		if outs[i].Code == 0 || code < outs[i].Code {
			outs[i].Code = code
			outs[i].Detail = detail
		}
	}

	winner := map[InstanceID]int{}
	losers := map[int]bool{}
	alive := map[int]bool{}
	directRoots := map[InstanceID]bool{}

	claim := func(id InstanceID, rootIdx int) {
		old, exists := winner[id]
		if !exists {
			winner[id] = rootIdx
			return
		}
		op, np := reqs[old].Priority, reqs[rootIdx].Priority
		if np < op || (np == op && rootIdx < old) {
			winner[id] = rootIdx
			losers[old] = true
			delete(losers, rootIdx)
		} else {
			losers[rootIdx] = true
		}
	}

	for i := range reqs {
		r := &reqs[i]
		in := ci[r.Instance]
		if in == nil {
			setErr(i, ErrUndeclared, "instance missing")
			continue
		}
		rule := m.types[in.typ].Rules[r.Rule]
		if rule == nil {
			setErr(i, ErrUndeclared, "rule not declared")
			continue
		}
		if !inStates(in.state, rule.From) {
			setErr(i, ErrUndeclared, "rule not allowed from state")
			continue
		}
		alive[i] = true
		directRoots[r.Instance] = true
		claim(r.Instance, i)
	}

	stepRule := map[InstanceID]*Rule{}
	for id, idx := range winner {
		stepRule[id] = m.types[ci[id].typ].Rules[reqs[idx].Rule]
	}
	m.expandCascades(winner, losers, reqs, ci, cl, stepRule, directRoots)
	// droppedEdge 记录“指向直接根实例、因而被丢弃”的级联边（src,link,dst）。
	droppedEdge := map[[2]InstanceID]bool{}
	for id, r := range stepRule {
		if r == nil {
			continue
		}
		for _, cas := range r.Cascades {
			for _, d := range neighbors(cl, id, cas.Link) {
				if wi, ok := winner[d]; ok && directRoots[d] && winner[id] != wi {
					droppedEdge[[2]InstanceID{id, d}] = true
				}
			}
		}
	}

	for {
		cover := map[InstanceID]int{}
		depth := map[InstanceID]int{}
		var walk func(InstanceID, int, int)
		walk = func(id InstanceID, root, d int) {
			if _, seen := cover[id]; seen {
				return
			}
			cover[id] = root
			depth[id] = d
			r := stepRule[id]
			if r == nil {
				return
			}
			for _, cas := range r.Cascades {
				for _, nb := range neighbors(cl, id, cas.Link) {
					if _, ok := winner[nb]; ok {
						if droppedEdge[[2]InstanceID{id, nb}] {
							continue
						}
						walk(nb, root, d+1)
					}
				}
			}
		}
		aliveIdx := make([]int, 0, len(alive))
		for i := range alive {
			aliveIdx = append(aliveIdx, i)
		}
		sortInts(aliveIdx)
		for _, i := range aliveIdx {
			if !losers[i] {
				walk(reqs[i].Instance, i, 0)
			}
		}

		for _, i := range aliveIdx {
			if !losers[i] && m.hasCycle(reqs[i].Instance, stepRule, cl) {
				setErr(i, ErrCycle, "cascade cycle")
				delete(alive, i)
			}
		}

		simCI, simCL := m.snapshot()
		fromState := map[InstanceID]State{}
		for id, in := range simCI {
			fromState[id] = in.state
		}
		ids := make([]InstanceID, 0, len(cover))
		for id := range cover {
			ids = append(ids, id)
		}
		sortStepsNaive(ids, depth)

		failRoot := map[int]int{}
		failDetail := map[int]string{}
		stepFail := map[InstanceID]int{}

		for _, id := range ids {
			root := cover[id]
			r := stepRule[id]
			if r == nil {
				stepFail[id] = ErrUndeclared
				failDetail[root] = "cascade rule undeclared"
				continue
			}
			in := simCI[id]
			terminal := inStates(in.state, m.types[in.typ].Terminals)
			if terminal {
				if !m.evalPre(id, r, simCI, simCL) {
					stepFail[id] = ErrPrecondition
					failDetail[root] = "precondition failed"
					continue
				}
				stepFail[id] = ErrTerminal
				failDetail[root] = "terminal instance"
				continue
			}
			// 记录本步骤生效前的属性与链接，失败时整体回退，避免污染
			// 同单元后续步骤看到“已推进但终将被拒”的中间投影。
			savedAttrs := map[AttrKey]AttrValue{}
			for k, vv := range in.attrs {
				savedAttrs[k] = vv
			}
			savedLinks := cloneLinks(simCL)
			var savedOut map[LinkType]map[InstanceID]bool
			if so, ok := savedLinks[id]; ok {
				savedOut = so
			}
			rollback := func(code int, detail string) {
				in.state = fromState[id]
				in.attrs = savedAttrs
				// 只回退本实例的出边。
				if saved, ok := savedLinks[id]; ok {
					_ = saved
					simCL[id] = cloneOut(savedOut)
				} else {
					delete(simCL, id)
				}
				stepFail[id] = code
				failRoot[root] = code
				failDetail[root] = detail
			}
			if owner := winner[id]; reqs[owner].Instance == id {
				for _, ao := range reqs[owner].Attrs {
					if ao.Op == "set" {
						in.attrs[ao.Key] = ao.Value
					} else {
						cur, _ := in.attrs[ao.Key].(int64)
						n, _ := ao.Value.(int64)
						in.attrs[ao.Key] = cur + n
					}
				}
				for _, lo := range reqs[owner].Links {
					ensureLink(simCL, id, lo.Link)
					if lo.Op == "add" {
						simCL[id][lo.Link][lo.Target] = true
					} else {
						delete(simCL[id][lo.Link], lo.Target)
					}
				}
			}
			if !m.evalPre(id, r, simCI, simCL) {
				rollback(ErrPrecondition, "precondition failed")
				continue
			}
			in.state = r.To
			if code := m.evalPost(id, r, simCI, simCL); code != 0 {
				rollback(code, "post-check failed")
				continue
			}
		}
		// 按覆盖归属把每个胜出步的错误汇总到其根（固定优先级取最严重）。
		for id, code := range stepFail {
			root := cover[id]
			if old, ok := failRoot[root]; !ok || code < old {
				failRoot[root] = code
				switch code {
				case ErrPrecondition:
					failDetail[root] = "precondition failed"
				case ErrCardinality, ErrHook:
					failDetail[root] = "post-check failed"
				case ErrTerminal:
					failDetail[root] = "terminal instance"
				default:
					failDetail[root] = "undeclared"
				}
			}
		}

		for i := range alive {
			if !losers[i] {
				continue
			}
			id := reqs[i].Instance
			// 落败迁移不会发生：其前置/基数/钩子不作为独立假设评估；
			// 仅在胜出方因更高优先级原因失败时跟随，否则保持未决最终报互斥。
			if code, ok := stepFail[id]; ok {
				// 落败迁移不会发生：只跟随比 ErrMutex(3) 更高优先级且与
				// “能否触发”相关的错误（未声明/前置条件/循环）。基数/钩子/
				// 终态属于胜出迁移自身的责任，落败根保持未决，最终报互斥。
				if code > ErrMutex {
					continue
				}
				setErr(i, code, failDetail[winner[id]])
				delete(alive, i)
			}
		}

		if len(failRoot) == 0 {
			m.insts = simCI
			m.links = simCL
			m.clock++
			for i := range alive {
				if losers[i] {
					setErr(i, ErrMutex, "lost arbitration")
				}
			}
			for i := range reqs {
				m.logf(reqs[i], outs[i])
			}
			return outs
		}
		for root, code := range failRoot {
			setErr(root, code, failDetail[root])
			delete(alive, root)
		}
	}
}

func (m *Model) expandCascades(
	winner map[InstanceID]int, losers map[int]bool, reqs []Request,
	ci map[InstanceID]*inst, cl map[InstanceID]map[LinkType]map[InstanceID]bool,
	stepRule map[InstanceID]*Rule, directRoots map[InstanceID]bool,
) {
	frontier := []InstanceID{}
	for id := range winner {
		frontier = append(frontier, id)
	}
	sort.Slice(frontier, func(i, j int) bool { return frontier[i] < frontier[j] })
	for len(frontier) > 0 {
		nextSet := map[InstanceID]bool{}
		for _, id := range frontier {
			r := stepRule[id]
			if r == nil {
				continue
			}
			for _, cas := range r.Cascades {
				for _, d := range neighbors(cl, id, cas.Link) {
					if ci[d] == nil {
						continue
					}
					nr := m.types[ci[d].typ].Rules[cas.ToRule]
					root := winner[id]
					_, exists := winner[d]
					// 已被直接根请求显式声明的实例不被级联覆盖。
					if exists && directRoots[d] {
						// 邻居被直接迁移：丢弃这条级联边，不拒绝发起根。
						continue
					}
					if !exists {
						winner[d] = root
						stepRule[d] = nr
						nextSet[d] = true
						continue
					}
					// 已有级联归属：共享该目标，保持原归属，不产生落败根。
				}
			}
		}
		frontier = frontier[:0]
		nf := make([]InstanceID, 0, len(nextSet))
		for id := range nextSet {
			nf = append(nf, id)
		}
		sort.Slice(nf, func(i, j int) bool { return nf[i] < nf[j] })
		frontier = append(frontier, nf...)
	}
}

func (m *Model) hasCycle(root InstanceID,
	stepRule map[InstanceID]*Rule, cl map[InstanceID]map[LinkType]map[InstanceID]bool) bool {
	color := map[InstanceID]int{}
	found := false
	var dfs func(InstanceID)
	dfs = func(id InstanceID) {
		if color[id] == 1 {
			found = true
			return
		}
		if color[id] == 2 {
			return
		}
		color[id] = 1
		if r := stepRule[id]; r != nil {
			for _, cas := range r.Cascades {
				for _, d := range neighbors(cl, id, cas.Link) {
					if _, ok := stepRule[d]; ok {
						dfs(d)
					}
				}
			}
		}
		color[id] = 2
	}
	dfs(root)
	return found
}

func sortStepsNaive(ids []InstanceID, depth map[InstanceID]int) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0; j-- {
			a, b := ids[j-1], ids[j]
			if depth[a] < depth[b] || (depth[a] == depth[b] && a < b) {
				break
			}
			ids[j-1], ids[j] = b, a
		}
	}
}
