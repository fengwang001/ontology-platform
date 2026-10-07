package orphanreclaim

import "sort"

// NaiveReclaimer 是独立维护全部入边与代际记录的朴素参考实现。
// 每次孤儿判定都逐条扫描该对象的全部入边，不做任何类型计数增量维护；
// 它故意「慢而直白」，仅用于随机操作序列下与正式引擎逐字段对照。
type NaiveReclaimer struct {
	cfg      Config
	now      int64
	alive    map[string]bool
	inEdges  map[string]map[string]map[string]bool // target -> source -> type -> present
	outEdges map[string]map[string]map[string]bool
	gen      map[string]Generation
	since    map[string]int64
	deadline map[string]int64
}

func NewNaive(cfg Config, start int64) *NaiveReclaimer {
	return &NaiveReclaimer{
		cfg:      cfg,
		now:      start,
		alive:    map[string]bool{},
		inEdges:  map[string]map[string]map[string]bool{},
		outEdges: map[string]map[string]map[string]bool{},
		gen:      map[string]Generation{},
		since:    map[string]int64{},
		deadline: map[string]int64{},
	}
}

func (n *NaiveReclaimer) SetTime(t int64) { n.now = t }

func (n *NaiveReclaimer) CreateObject(id string) {
	n.alive[id] = true
}

func (n *NaiveReclaimer) AddLink(source, target, linkType string) error {
	if !n.alive[target] || !n.alive[source] {
		return ErrObjectNotFound
	}
	if _, ok := n.cfg.Rules[linkType]; !ok {
		return ErrLinkTypeUnconfigured
	}
	n.setEdge(source, target, linkType, true)
	n.reassess(target)
	return nil
}

func (n *NaiveReclaimer) RemoveLink(source, target, linkType string) error {
	if !n.alive[target] {
		return ErrObjectNotFound
	}
	if _, ok := n.cfg.Rules[linkType]; !ok {
		return ErrLinkTypeUnconfigured
	}
	n.setEdge(source, target, linkType, false)
	n.reassess(target)
	return nil
}

func (n *NaiveReclaimer) setEdge(source, target, linkType string, present bool) {
	if n.inEdges[target] == nil {
		n.inEdges[target] = map[string]map[string]bool{}
	}
	if n.inEdges[target][source] == nil {
		n.inEdges[target][source] = map[string]bool{}
	}
	if present {
		n.inEdges[target][source][linkType] = true
	} else {
		delete(n.inEdges[target][source], linkType)
		if len(n.inEdges[target][source]) == 0 {
			delete(n.inEdges[target], source)
		}
	}
	if n.outEdges[source] == nil {
		n.outEdges[source] = map[string]map[string]bool{}
	}
	if n.outEdges[source][target] == nil {
		n.outEdges[source][target] = map[string]bool{}
	}
	if present {
		n.outEdges[source][target][linkType] = true
	} else {
		delete(n.outEdges[source][target], linkType)
		if len(n.outEdges[source][target]) == 0 {
			delete(n.outEdges[source], target)
		}
	}
}

// isOrphanNaive 逐条枚举全部入边（线性扫描），再按固定两层规则判定。
func (n *NaiveReclaimer) isOrphanNaive(id string) bool {
	typesPresent := map[string]bool{}
	edgeCount := 0
	for _, byType := range n.inEdges[id] {
		for t, present := range byType {
			if present {
				typesPresent[t] = true
				edgeCount++
			}
		}
	}
	_ = edgeCount

	var independents, jointMembers []string
	for name, rule := range n.cfg.Rules {
		if rule.Kind == IndependentRetention {
			independents = append(independents, name)
		}
		if rule.Kind == JointRetention {
			jointMembers = append(jointMembers, name)
		}
	}
	sort.Strings(independents)
	for _, t := range independents {
		if typesPresent[t] {
			return false
		}
	}
	// 并查集复刻正式配置的分组合并语义。
	groups := n.jointGroups(jointMembers)
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	for _, g := range groups {
		all := true
		for _, t := range g {
			if !typesPresent[t] {
				all = false
				break
			}
		}
		if all {
			return false
		}
	}
	return true
}

func (n *NaiveReclaimer) jointGroups(members []string) [][]string {
	uf := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if uf[x] != x {
			uf[x] = find(uf[x])
		}
		return uf[x]
	}
	for _, name := range members {
		uf[name] = name
	}
	for _, name := range members {
		for _, ref := range n.cfg.Rules[name].JointGroup {
			if _, ok := uf[ref]; !ok {
				uf[ref] = ref
			}
			ra, rb := find(name), find(ref)
			if ra < rb {
				uf[rb] = ra
			} else {
				uf[ra] = rb
			}
		}
	}
	byRoot := map[string][]string{}
	for x := range uf {
		byRoot[find(x)] = append(byRoot[find(x)], x)
	}
	var out [][]string
	for _, g := range byRoot {
		sort.Strings(g)
		out = append(out, g)
	}
	return out
}

func (n *NaiveReclaimer) reassess(id string) {
	prev := n.gen[id]
	if !n.isOrphanNaive(id) {
		if prev != GenNone {
			delete(n.gen, id)
			delete(n.since, id)
			delete(n.deadline, id)
		}
		return
	}
	if prev == GenNone {
		n.gen[id] = Gen1
		n.since[id] = n.now
		n.deadline[id] = n.now + n.cfg.GraceGen1
	}
}

func (n *NaiveReclaimer) Advance() {
	// Gen2 清理优先。
	var gen2Due []string
	for id, g := range n.gen {
		if g == Gen2 && n.deadline[id] <= n.now {
			gen2Due = append(gen2Due, id)
		}
	}
	sort.Strings(gen2Due)
	for _, id := range gen2Due {
		if !n.isOrphanNaive(id) {
			delete(n.gen, id)
			delete(n.since, id)
			delete(n.deadline, id)
			continue
		}
		n.purge(id)
	}

	var gen1Due []string
	for id, g := range n.gen {
		if g == Gen1 && n.deadline[id] <= n.now {
			gen1Due = append(gen1Due, id)
		}
	}
	sort.Strings(gen1Due)
	for _, id := range gen1Due {
		if !n.isOrphanNaive(id) {
			delete(n.gen, id)
			delete(n.since, id)
			delete(n.deadline, id)
			continue
		}
		n.gen[id] = Gen2
		n.since[id] = n.now
		n.deadline[id] = n.now + n.cfg.GraceGen2
	}
}

func (n *NaiveReclaimer) purge(id string) {
	// 原子摘除出边并删除对象，随后按同一套规则重评受影响目标。
	var affected []string
	for tgt, byType := range n.outEdges[id] {
		affected = append(affected, tgt)
		for t, present := range byType {
			if present {
				delete(n.inEdges[tgt][id], t)
				if len(n.inEdges[tgt][id]) == 0 {
					delete(n.inEdges[tgt], id)
				}
			}
		}
	}
	delete(n.outEdges, id)
	delete(n.inEdges, id)
	delete(n.alive, id)
	delete(n.gen, id)
	delete(n.since, id)
	delete(n.deadline, id)
	sort.Strings(affected)
	for _, tgt := range affected {
		if n.alive[tgt] && n.gen[tgt] == GenNone {
			n.reassess(tgt)
		}
	}
}

// Snapshot 导出与正式引擎同构的状态快照。
func (n *NaiveReclaimer) Snapshot() Snapshot {
	s := Snapshot{
		At:       n.now,
		Alive:    map[string]Generation{},
		Since:    map[string]int64{},
		Deadline: map[string]int64{},
		InEdges:  map[string]map[string][]string{},
		OutEdges: map[string]map[string][]string{},
	}
	for id := range n.alive {
		g := n.gen[id]
		if g == 0 {
			g = GenNone
		}
		s.Alive[id] = g
		if g != GenNone {
			s.Since[id] = n.since[id]
			s.Deadline[id] = n.deadline[id]
			if g == Gen1 {
				s.Gen1Queue = append(s.Gen1Queue, id)
			} else {
				s.Gen2Queue = append(s.Gen2Queue, id)
			}
		}
		in := map[string][]string{}
		for src, byType := range n.inEdges[id] {
			var ts []string
			for t, present := range byType {
				if present {
					ts = append(ts, t)
				}
			}
			sort.Strings(ts)
			in[src] = ts
		}
		s.InEdges[id] = in
		out := map[string][]string{}
		for tgt, byType := range n.outEdges[id] {
			var ts []string
			for t, present := range byType {
				if present {
					ts = append(ts, t)
				}
			}
			sort.Strings(ts)
			out[tgt] = ts
		}
		s.OutEdges[id] = out
	}
	sort.Strings(s.Gen1Queue)
	sort.Strings(s.Gen2Queue)
	return s
}
