package alloc_test

import (
	"fmt"
	"sort"
	"strings"

	"ontology/alloc"
	"ontology/decider"
)

// simCopy 是朴素模拟器中的一份。
type simCopy struct {
	index   string
	shard   int
	primary bool
}

type simNode struct {
	id       string
	zone     string
	total    int64
	other    int64
	excluded bool
	copies   map[string]map[int]bool // index -> shard set（主或副，每分片至多一份）
}

type simShard struct {
	primary  string
	replicas map[string]bool
}

type simIndex struct {
	s, r  int
	size  int64
	shard []*simShard
}

// sim 是完全按题面规则逐份、逐节点重放的朴素实现，不引用生产代码的决策路径。
type sim struct {
	l, h   int
	nodes  map[string]*simNode
	order  []string
	idx    map[string]*simIndex
	log    []string
	trials int // 最近一次 Reroute 的"尝试份数×节点"候选搜索次数
}

func newSim(l, h int) *sim {
	return &sim{l: l, h: h, nodes: map[string]*simNode{}, idx: map[string]*simIndex{}}
}

func (m *sim) addNode(id, zone string, total int64) {
	if _, ok := m.nodes[id]; ok {
		return
	}
	m.nodes[id] = &simNode{id: id, zone: zone, total: total, copies: map[string]map[int]bool{}}
	m.order = append(m.order, id)
}

func (m *sim) setOther(id string, v int64) {
	if n, ok := m.nodes[id]; ok {
		n.other = v
	}
}

func (m *sim) setExclude(id string, on bool) {
	if n, ok := m.nodes[id]; ok {
		n.excluded = on
	}
}

func (m *sim) createIndex(name string, s, r int, size int64) {
	if _, ok := m.idx[name]; ok {
		return
	}
	ix := &simIndex{s: s, r: r, size: size, shard: make([]*simShard, s)}
	for i := range ix.shard {
		ix.shard[i] = &simShard{replicas: map[string]bool{}}
	}
	m.idx[name] = ix
}

func (m *sim) used(n *simNode) int64 {
	u := n.other
	for name, set := range n.copies {
		u += int64(len(set)) * m.idx[name].size
	}
	return u
}

func (m *sim) zoneCount() int {
	z := map[string]bool{}
	for _, n := range m.nodes {
		z[n.zone] = true
	}
	return len(z)
}

func (m *sim) zoneCopies(zone, index string, s int) int {
	cnt := 0
	for _, n := range m.nodes {
		if n.zone != zone {
			continue
		}
		if n.copies[index][s] {
			cnt++
		}
	}
	return cnt
}

// eval 严格按 D1→D4 评估；take 为"正从源拿走"的那份（可零值）。
func (m *sim) eval(n *simNode, index string, s int, size int64, primary, lowWater bool,
	take simCopy, takeNode string) decider.Reason {

	has := n.copies[index][s]
	if takeNode == n.id && take.index == index && take.shard == s {
		has = false
	}
	u := m.used(n)
	if takeNode == n.id {
		u -= m.idx[take.index].size
	}

	if n.excluded {
		return decider.D1Excluded
	}
	if has {
		return decider.D2SameShard
	}
	z := m.zoneCount()
	limit := (1 + m.idx[index].r + z - 1) / z
	zc := m.zoneCopies(n.zone, index, s)
	if take.index == index && take.shard == s && takeNode != "" {
		if m.nodes[takeNode].zone == n.zone {
			zc--
		}
	}
	if zc+1 > limit {
		return decider.D3ZoneAwareness
	}
	pct := m.l
	if primary && !lowWater {
		pct = m.h
	}
	if (u+size)*100 > int64(pct)*n.total {
		return decider.D4Disk
	}
	return decider.Pass
}

func (m *sim) sortedNodeIDs() []string {
	ids := append([]string{}, m.order...)
	sort.Strings(ids)
	return ids
}

// choose 返回通过全部规则的节点中份数最少、id 最小者。
func (m *sim) choose(index string, s int, size int64, primary, lowWater bool,
	skip string, take simCopy, takeNode string) (string, decider.Reason, bool) {

	m.trials++
	best, bestCnt, bestReason := "", 0, decider.Pass
	for _, id := range m.sortedNodeIDs() {
		if id == skip {
			continue
		}
		n := m.nodes[id]
		r := m.eval(n, index, s, size, primary, lowWater, take, takeNode)
		if r != decider.Pass {
			continue
		}
		cnt := 0
		for _, set := range n.copies {
			cnt += len(set)
		}
		if best == "" || cnt < bestCnt {
			best, bestCnt, bestReason = id, cnt, r
		}
	}
	return best, bestReason, best != ""
}

func (m *sim) put(nid, index string, s int, primary bool) {
	n := m.nodes[nid]
	set := n.copies[index]
	if set == nil {
		set = map[int]bool{}
		n.copies[index] = set
	}
	set[s] = true
	sh := m.idx[index].shard[s]
	if primary {
		sh.primary = nid
	} else {
		sh.replicas[nid] = true
	}
}

// reroute 与 alloc.Reroute 同规则，返回 Assigned、Moved。
func (m *sim) reroute() ([]alloc.Item, []alloc.Item) {
	m.trials = 0
	var assigned, moved []alloc.Item

	names := make([]string, 0, len(m.idx))
	for name := range m.idx {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ix := m.idx[name]
		for s := 0; s < ix.s; s++ {
			sh := ix.shard[s]
			if sh.primary == "" {
				if dst, _, ok := m.choose(name, s, ix.size, true, false, "", simCopy{}, ""); ok {
					m.put(dst, name, s, true)
					assigned = append(assigned, alloc.Item{Index: name, Shard: s, Primary: true, To: dst})
				}
			}
			if sh.primary == "" {
				m.log = append(m.log, fmt.Sprintf("skip replicas %s/%d: primary not ready", name, s))
				continue
			}
			// 逐份尝试所有缺失副本位；某位无候选则该份保持未分配并停止该分片后续位。
			for k := len(sh.replicas); k < ix.r; k++ {
				dst, _, ok := m.choose(name, s, ix.size, false, false, "", simCopy{}, "")
				if !ok {
					m.log = append(m.log, fmt.Sprintf("replica %s/%d#%d unassigned: no candidate",
						name, s, k))
					break
				}
				m.put(dst, name, s, false)
				assigned = append(assigned, alloc.Item{Index: name, Shard: s, Primary: false, To: dst})
			}
		}
	}

	for _, nid := range m.sortedNodeIDs() {
		n := m.nodes[nid]
		overHigh := func() bool { return m.used(n)*100 > int64(m.h)*n.total }
		if !n.excluded && !overHigh() {
			continue
		}
		cps := make([]simCopy, 0)
		for ixName, set := range n.copies {
			for s := range set {
				sh := m.idx[ixName].shard[s]
				cps = append(cps, simCopy{
					index: ixName, shard: s, primary: sh.primary == n.id,
				})
			}
		}
		sort.Slice(cps, func(i, j int) bool {
			si, sj := m.idx[cps[i].index].size, m.idx[cps[j].index].size
			if si != sj {
				return si > sj
			}
			if cps[i].index != cps[j].index {
				return cps[i].index < cps[j].index
			}
			return cps[i].shard < cps[j].shard
		})
		for _, cp := range cps {
			if !n.excluded && !overHigh() {
				break
			}
			ix := m.idx[cp.index]
			dst, _, ok := m.choose(cp.index, cp.shard, ix.size, cp.primary, true,
				nid, cp, nid)
			if !ok {
				m.log = append(m.log, fmt.Sprintf("move %s/%d primary=%v stuck on %s",
					cp.index, cp.shard, cp.primary, nid))
				continue
			}
			delete(n.copies[cp.index], cp.shard)
			if len(n.copies[cp.index]) == 0 {
				delete(n.copies, cp.index)
			}
			if cp.primary {
				m.idx[cp.index].shard[cp.shard].primary = ""
			} else {
				delete(m.idx[cp.index].shard[cp.shard].replicas, nid)
			}
			m.put(dst, cp.index, cp.shard, cp.primary)
			moved = append(moved, alloc.Item{
				Index: cp.index, Shard: cp.shard, Primary: cp.primary, From: nid, To: dst,
			})
			m.log = append(m.log, fmt.Sprintf("moved %s/%d primary=%v %s->%s",
				cp.index, cp.shard, cp.primary, nid, dst))
		}
	}
	return assigned, moved
}

func (m *sim) explain(index string, s int, primary bool) ([]alloc.Verdict, bool, bool) {
	ix := m.idx[index]
	verdicts := []alloc.Verdict{}
	for _, id := range m.sortedNodeIDs() {
		n := m.nodes[id]
		r := m.eval(n, index, s, ix.size, primary, false, simCopy{}, "")
		verdicts = append(verdicts, alloc.Verdict{Node: id, Rule: r, Pass: r == decider.Pass})
	}
	notReady := !primary && ix.shard[s].primary == ""
	return verdicts, notReady, true
}

// needsWork 报告是否存在未分配份、排除非空节点或超高水位节点。
func (m *sim) needsWork() bool {
	for name, ix := range m.idx {
		for s, sh := range ix.shard {
			if sh.primary == "" || len(sh.replicas) < ix.r {
				m.log = append(m.log, fmt.Sprintf("unassigned: %s/%d primary=%q reps=%d/%d",
					name, s, sh.primary, len(sh.replicas), ix.r))
				return true
			}
		}
	}
	for _, n := range m.nodes {
		if n.excluded {
			for _, set := range n.copies {
				if len(set) > 0 {
					return true
				}
			}
		}
		if m.used(n)*100 > int64(m.h)*n.total {
			return true
		}
	}
	return false
}

func itemsKey(xs []alloc.Item) string {
	var b strings.Builder
	for _, x := range xs {
		fmt.Fprintf(&b, "%s/%d/%v/%s->%s;", x.Index, x.Shard, x.Primary, x.From, x.To)
	}
	return b.String()
}
