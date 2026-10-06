package delay

import "sort"

// acState 是扫描过程中一架飞机的滚动状态（=其链上最近一段之后的状态）。
type acState struct {
	readySet bool   // 是否存在“最早可用时刻”约束
	ready    int    // 实际到达 + 最短过站
	airport  string // 当前所在机场
	root     *croot // 非 nil 表示飞机滞留（机场不匹配即无飞机可用）
}

// crState 是扫描过程中一组机组的滚动状态。
type crState struct {
	readySet bool
	ready    int
	airport  string
	root     *croot
	dead     *croot // 值勤失效根；非 nil 后后续段一律无机组
	firstDep int    // 第一个实际起飞段的实际起飞时刻
	firstSet bool
}

// recompute 以 seeds 为变化起点做增量重算；seeds==nil 表示整表重算。
// freezeAt>=0 时，实际起飞时刻不大于该时刻且未取消的航班结论冻结，
// 其结论与资源状态均不被改动。
func (e *Engine) recompute(seeds map[string]bool, freezeAt int) {
	affected := e.affectedSet(seeds, freezeAt)

	// 全局按（计划起飞，输入序号）扫描，与链条排序一致，
	// 从而保证任一航班被处理时其飞机/机组前驱均已处理。
	order := make([]*flight, 0, len(e.flights))
	for _, f := range e.flights {
		if affected[f.ID] {
			order = append(order, f)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].Scheduled != order[j].Scheduled {
			return order[i].Scheduled < order[j].Scheduled
		}
		return order[i].ord < order[j].ord
	})

	acs := make(map[string]*acState)
	crs := make(map[string]*crState)
	if e.conc == nil {
		e.conc = make(map[string]*conclusion)
	}

	e.lastSwept = 0
	for _, f := range order {
		e.lastSwept++
		a := e.acStateFor(f, acs)
		c := e.crStateFor(f, crs)
		e.process(f, a, c)
	}
}

// affectedSet 由变化种子沿两条链向下游传播得到“结论可能改变”的集合。
// 仅遍历可达航班，规模与受影响航班数同阶。
func (e *Engine) affectedSet(seeds map[string]bool, freezeAt int) map[string]bool {
	set := make(map[string]bool)
	if seeds == nil {
		for _, f := range e.flights {
			set[f.ID] = true
		}
		return set
	}
	var stack []*flight
	for id := range seeds {
		if f, ok := e.byID[id]; ok {
			stack = append(stack, f)
		}
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if set[f.ID] {
			continue
		}
		if e.isFrozen(f, freezeAt) {
			continue // 已起飞航班结论固定，且阻断继续传播
		}
		set[f.ID] = true
		if la := e.chainsA[f.AircraftID]; f.piA+1 < len(la) {
			stack = append(stack, la[f.piA+1])
		}
		if lc := e.chainsC[f.CrewID]; f.piC+1 < len(lc) {
			stack = append(stack, lc[f.piC+1])
		}
	}
	return set
}

func (e *Engine) isFrozen(f *flight, freezeAt int) bool {
	if freezeAt < 0 {
		return false
	}
	c := e.conc[f.ID]
	if c == nil {
		return false
	}
	return c.status != StatusCanceled && c.dep <= freezeAt
}

// acStateFor 懒初始化某飞机在处理 f 时的滚动状态：
// 链头为“无时刻约束、在 f 起飞机场”；否则取直接前驱存储结论重建，
// 前驱结论已包含其之前所有传导，故无需再向前回溯。
func (e *Engine) acStateFor(f *flight, m map[string]*acState) *acState {
	if s, ok := m[f.AircraftID]; ok {
		return s
	}
	s := &acState{}
	if f.piA == 0 {
		s.airport = f.Origin
	} else {
		p := e.chainsA[f.AircraftID][f.piA-1]
		pc := e.conc[p.ID]
		s.readySet = pc.readyASet
		s.ready = pc.readyA
		if pc.rootA != nil {
			s.airport = pc.airportA
			s.root = pc.rootA
		} else {
			s.airport = p.Dest
		}
	}
	m[f.AircraftID] = s
	return s
}

// crStateFor 懒初始化某机组在处理 f 时的滚动状态（规则同飞机，
// 另需恢复值勤失效标记与首个实际起飞段）。
func (e *Engine) crStateFor(f *flight, m map[string]*crState) *crState {
	if s, ok := m[f.CrewID]; ok {
		return s
	}
	s := &crState{}
	if f.piC == 0 {
		s.airport = f.Origin
	} else {
		p := e.chainsC[f.CrewID][f.piC-1]
		pc := e.conc[p.ID]
		s.dead = pc.deadC
		s.firstDep = pc.firstDepC
		s.firstSet = pc.firstSetC
		s.readySet = pc.readyCSet
		s.ready = pc.readyC
		if pc.rootC != nil {
			s.airport = pc.airportC
			s.root = pc.rootC
		} else {
			s.airport = p.Dest
		}
	}
	m[f.CrewID] = s
	return s
}

// earlierRoot 在两个可能为 nil 的根中取传导链上最早者。
func earlierRoot(a, b *croot) *croot {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.earlier(b) {
		return a
	}
	return b
}

// process 按规则推算单个航班，并把资源滚动状态与持久结论同时更新。
func (e *Engine) process(f *flight, a *acState, c *crState) {
	delay := 0
	injected := false
	for _, in := range e.inj[f.ID] {
		if in.Cancel {
			injected = true
		} else {
			delay += in.Delay
		}
	}

	// 1) 注入取消优先级最高（根锚定时刻 -1）。
	if injected {
		root := &croot{time: -1, rank: reasonRank[ReasonInjected], reason: ReasonInjected, birth: f.ord}
		e.cancel(f, a, c, root, ReasonInjected)
		return
	}

	// 2) 资源滞留 / 值勤失效导致的传导取消，取最早根。
	// 资源一旦在与其当前所在机场相同的后续段处被处理，滞留即解除
	// （机场匹配意味着资源重新可用），该段之后继续正常传导。
	if a.root != nil && a.airport == f.Origin {
		a.root = nil
	}
	if c.root != nil && c.airport == f.Origin && c.dead == nil {
		c.root = nil
	}
	var block *croot
	var blockKind Reason
	addBlock := func(r *croot, kind Reason) {
		if r == nil {
			return
		}
		strictlyEarlier := block == nil || r.earlier(block)
		equivalent := block != nil && !r.earlier(block) && !block.earlier(r)
		if strictlyEarlier ||
			(equivalent && reasonRank[kind] < reasonRank[blockKind]) {
			block = r
			blockKind = kind
		}
	}
	// 飞机滞留、机组滞留、值勤失效统一按根事件时间取最早；
	// 完全相同的根同时命中两侧时，按固定原因秩取靠前类别。
	// 值勤失效（deadC）不在此处阻断：它表示该机组“在场但不能再飞”，
	// 须在时刻推算后作为值勤检查处理；若飞机也不在场则无飞机优先。
	if a.root != nil && a.airport != f.Origin {
		addBlock(a.root, ReasonNoAircraft)
	}
	if c.dead != nil {
		addBlock(c.dead, ReasonDutyExceeded)
	} else if c.root != nil && c.airport != f.Origin {
		addBlock(c.root, ReasonNoCrew)
	}
	if block != nil {
		e.cancel(f, a, c, block, blockKind)
		return
	}

	// 3) 推算实际起飞：计划+注入延误、飞机过站、机组衔接三者最大值。
	dep := f.Scheduled + delay
	if a.readySet && a.ready > dep {
		dep = a.ready
	}
	if c.readySet && c.ready > dep {
		dep = c.ready
	}

	// 4) 起飞落入起飞机场宵禁（左闭右开）则推迟到宵禁结束。
	if cf := e.cfg.Curfews[f.Origin]; cf.Contains(dep) {
		dep = cf.End
	}
	arr := dep + f.Duration

	// 机组第一个未取消段的实际起飞，作为值勤跨度起点。
	firstDep := c.firstDep
	if !c.firstSet {
		firstDep = dep
	}

	// 5) 到达落入到达机场宵禁则取消（宵禁原因先于值勤判定）。
	if cf := e.cfg.Curfews[f.Dest]; cf.Contains(arr) {
		root := &croot{time: arr, rank: reasonRank[ReasonCurfew], reason: ReasonCurfew, birth: f.ord}
		e.cancel(f, a, c, root, ReasonCurfew)
		return
	}

	// 6) 值勤跨度：恰等于上限允许，超过则该段及机组链后续全部取消。
	// 机组一旦值勤失效（deadC），即使其所在机场匹配也不能再执行，
	// 该段与机组链后续均以“值勤超限”取消。
	if c.dead != nil || arr-firstDep > e.cfg.DutyLimit {
		root := c.dead
		if root == nil {
			root = &croot{time: arr, rank: reasonRank[ReasonDutyExceeded],
				reason: ReasonDutyExceeded, birth: f.ord}
		}
		e.cancel(f, a, c, root, ReasonDutyExceeded)
		return
	}

	// 7) 正常执行。
	if !c.firstSet {
		c.firstDep = firstDep
		c.firstSet = true
	}
	a.readySet = true
	a.ready = arr + e.cfg.MinTurnaround
	a.airport = f.Dest
	a.root = nil
	c.readySet = true
	c.ready = arr + e.cfg.MinConnection
	c.airport = f.Dest
	c.root = nil

	status := StatusScheduled
	if dep != f.Scheduled {
		status = StatusDelayed
	}
	e.conc[f.ID] = &conclusion{
		status:    status,
		dep:       dep,
		arr:       arr,
		reason:    ReasonNone,
		rootA:     nil,
		rootC:     nil,
		airportA:  f.Dest,
		airportC:  f.Dest,
		readyASet: true,
		readyA:    arr + e.cfg.MinTurnaround,
		readyCSet: true,
		readyC:    arr + e.cfg.MinConnection,
		deadC:     c.dead,
		firstDepC: c.firstDep,
		firstSetC: c.firstSet,
	}
}

// cancel 记录取消结论并推进资源状态：
// 飞机与机组均停留在该航班起飞机场；值勤根额外使机组永久失效。
func (e *Engine) cancel(f *flight, a *acState, c *crState, root *croot, kind Reason) {
	// 取消段的统一资源转移（后状态 = 资源“能到达哪里”）：
	// 飞机：若其滞留机场 != 本段起飞机场，则飞机到不了，保持原机场与根；
	//      否则飞机停在本段起飞机场。原发取消（注入/宵禁）挂本根，
	//      纯传导/无机组原因不产生飞机侧根（rootA=nil）。
	// 机组：对称；值勤根额外置 dead，且值勤/无机组原因挂机组侧根，
	//      注入/宵禁也挂机组侧根。
	prevReadyASet, prevReadyA := a.readySet, a.ready
	prevReadyCSet, prevReadyC := c.readySet, c.ready
	rootA := a.root
	rootC := c.root

	aAbsent := a.root != nil && a.airport != f.Origin
	cAbsent := c.root != nil && c.airport != f.Origin
	if !aAbsent {
		a.airport = f.Origin
		switch kind {
		case ReasonInjected, ReasonCurfew:
			a.root = root
			rootA = root
		default:
			a.root = nil
			rootA = nil
		}
	}
	if !cAbsent {
		c.airport = f.Origin
		switch kind {
		case ReasonInjected, ReasonCurfew, ReasonDutyExceeded, ReasonNoCrew:
			c.root = root
			rootC = root
		default:
			c.root = nil
			rootC = nil
		}
	}
	if root.reason == ReasonDutyExceeded {
		c.dead = root
	}
	e.persistCancel(f, a, c, kind, root, rootA, rootC,
		prevReadyASet, prevReadyA, prevReadyCSet, prevReadyC)
}

func (e *Engine) persistCancel(f *flight, a *acState, c *crState, kind Reason,
	root, rootA, rootC *croot, raSet bool, ra int, rcSet bool, rc int) {
	e.conc[f.ID] = &conclusion{
		status:    StatusCanceled,
		reason:    kind,
		root:      root,
		rootA:     rootA,
		rootC:     rootC,
		airportA:  a.airport,
		airportC:  c.airport,
		readyASet: raSet,
		readyA:    ra,
		readyCSet: rcSet,
		readyC:    rc,
		deadC:     c.dead,
		firstDepC: c.firstDep,
		firstSetC: c.firstSet,
	}
}
