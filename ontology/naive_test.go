package ontology

// naiveCore 是一个独立的朴素串行参照实现，用于并发对照测试。
// 它不使用任何增量维护结构：每次上限变化都全量扫描并重算待处理集合，
// 语义以最直接的方式声明：待处理集合 = 已登记且未固定链接中
// 按 (Seq, ID) 最新的 excess 条。
type naiveCore struct {
	limits  [2]int
	groups  map[string]*naiveGroup // key: "dir|objID"
	links   map[string]*naiveLink
	deleted map[string]bool
	seq     uint64
}

type naiveLink struct {
	id     string
	source string
	target string
	seq    uint64
}

type naiveGroup struct {
	order    []string
	pending  map[string]bool
	pinned   map[string]bool
	override int
}

func newNaiveCore(srcLimit, tgtLimit int) *naiveCore {
	return &naiveCore{
		limits:  [2]int{srcLimit, tgtLimit},
		groups:  make(map[string]*naiveGroup),
		links:   make(map[string]*naiveLink),
		deleted: make(map[string]bool),
	}
}

func naiveKey(dir Direction, obj string) string {
	if dir == DirectionOut {
		return "o|" + obj
	}
	return "i|" + obj
}

func (n *naiveCore) group(dir Direction, obj string, create bool) *naiveGroup {
	k := naiveKey(dir, obj)
	g := n.groups[k]
	if g == nil && create {
		g = &naiveGroup{pending: make(map[string]bool), pinned: make(map[string]bool)}
		n.groups[k] = g
	}
	return g
}

func (n *naiveCore) effLimit(dir Direction, g *naiveGroup) int {
	base := n.limits[dir]
	if base == Unlimited {
		return Unlimited
	}
	return base + g.override
}

// create 返回是否接受。只统计有效链接（登记数减去待处理数）。
func (n *naiveCore) create(id, source, target string) bool {
	if n.links[id] != nil || n.deleted[id] {
		return false
	}
	objs := [2]string{source, target}
	for dir := DirectionOut; dir <= DirectionIn; dir++ {
		g := n.group(dir, objs[dir], false)
		if g == nil {
			continue
		}
		eff := n.effLimit(dir, g)
		if eff != Unlimited && len(g.order)-len(g.pending) >= eff {
			return false
		}
	}
	n.seq++
	n.links[id] = &naiveLink{id: id, source: source, target: target, seq: n.seq}
	for dir := DirectionOut; dir <= DirectionIn; dir++ {
		g := n.group(dir, objs[dir], true)
		g.order = append(g.order, id)
	}
	return true
}

func (n *naiveCore) setLimit(dir Direction, limit int) {
	n.limits[dir] = limit
	for key, g := range n.groups {
		if (dir == DirectionOut) == (key[0] == 'o') {
			n.recompute(dir, g)
		}
	}
}

// recompute 全量重算：待处理集合 = 最新的 excess 条未固定链接。
func (n *naiveCore) recompute(dir Direction, g *naiveGroup) {
	eff := n.effLimit(dir, g)
	excess := 0
	if eff != Unlimited && len(g.order) > eff {
		excess = len(g.order) - eff
	}
	g.pending = make(map[string]bool)
	for i := len(g.order) - 1; i >= 0 && excess > 0; i-- {
		id := g.order[i]
		if g.pinned[id] {
			continue
		}
		g.pending[id] = true
		excess--
	}
}

// pendingLinks 返回当前处于待处理状态的链接集合（任一方向待处理即算）。
func (n *naiveCore) pendingLinks() map[string]bool {
	out := make(map[string]bool)
	for _, g := range n.groups {
		for id := range g.pending {
			out[id] = true
		}
	}
	return out
}
