package scaledown

import "sort"

// naivePod / naiveNode 是与生产实现刻意异构的朴素数据结构（结构体切片 + 线性查找），
// 逐字照抄题目规则，作为差分测试的参照模型。
type naivePod struct {
	id   string
	node string
	pc   int64
	pm   int64
	kind string
}

type naiveNode struct {
	name  string
	ca    int64
	ma    int64
	since int64
	has   bool
}

type naiveSim struct {
	p        int
	t        int64
	minNodes int
	nodes    []naiveNode
	pods     []naivePod
	maxNow   int64
	seen     bool
}

func naiveNew(p int, t int64, min int) *naiveSim {
	if p < 1 || p > 100 || t < 1 || min < 0 || min > 1_000_000 {
		return nil
	}
	return &naiveSim{p: p, t: t, minNodes: min}
}

func (m *naiveSim) nodeIndex(name string) int {
	for i := range m.nodes {
		if m.nodes[i].name == name {
			return i
		}
	}
	return -1
}

func (m *naiveSim) podIndex(id string) int {
	for i := range m.pods {
		if m.pods[i].id == id {
			return i
		}
	}
	return -1
}

func (m *naiveSim) addNode(name string, ca, ma int64) error {
	if name == "" || ca < 1 || ca > 1e12 || ma < 1 || ma > 1e12 {
		return ErrInvalidArg
	}
	if m.nodeIndex(name) >= 0 {
		return ErrNodeExists
	}
	m.nodes = append(m.nodes, naiveNode{name: name, ca: ca, ma: ma})
	return nil
}

func (m *naiveSim) used(node string) (cpu, mem int64) {
	for _, p := range m.pods {
		if p.node == node {
			cpu += p.pc
			mem += p.pm
		}
	}
	return
}

func (m *naiveSim) addPod(p Pod) error {
	validKind := p.Kind == KindNormal || p.Kind == KindDaemon || p.Kind == KindPinned
	if p.ID == "" || !validKind || p.PC < 0 || p.PC > 1e12 || p.PM < 0 || p.PM > 1e12 ||
		(p.PC == 0 && p.PM == 0) {
		return ErrInvalidArg
	}
	if m.podIndex(p.ID) >= 0 {
		return ErrPodExists
	}
	ni := m.nodeIndex(p.Node)
	if ni < 0 {
		return ErrNodeNotFound
	}
	cpu, mem := m.used(p.Node)
	if cpu+p.PC > m.nodes[ni].ca || mem+p.PM > m.nodes[ni].ma {
		return ErrCapacity
	}
	m.pods = append(m.pods, naivePod{p.ID, p.Node, p.PC, p.PM, p.Kind})
	return nil
}

func (m *naiveSim) removePod(id string) error {
	i := m.podIndex(id)
	if i < 0 {
		return ErrPodNotFound
	}
	m.pods = append(m.pods[:i], m.pods[i+1:]...)
	return nil
}

func sortedNames(nodes []naiveNode) []string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.name)
	}
	sort.Strings(names)
	return names
}

func (m *naiveSim) tick(now int64) (TickResult, error) {
	if now < 0 {
		return TickResult{}, ErrInvalidArg
	}
	if m.seen && now < m.maxNow {
		return TickResult{}, ErrClockRewind
	}

	type placement struct{ id, target string }
	removable := map[string]bool{}
	inCPU := map[string]int64{}
	inMem := map[string]int64{}
	plans := map[string][]placement{}

	for _, v := range sortedNames(m.nodes) {
		var sc, sm int64
		pinned := false
		var normals []naivePod
		for _, p := range m.pods {
			if p.node != v {
				continue
			}
			if p.kind == KindDaemon {
				continue
			}
			sc += p.pc
			sm += p.pm
			if p.kind == KindPinned {
				pinned = true
			} else {
				normals = append(normals, p)
			}
		}
		ni := m.nodeIndex(v)
		ok := sc*100 < int64(m.p)*m.nodes[ni].ca && sm*100 < int64(m.p)*m.nodes[ni].ma
		if ok && (inCPU[v] > 0 || inMem[v] > 0) {
			ok = false
		}
		if ok && pinned {
			ok = false
		}

		trialCPU := map[string]int64{}
		trialMem := map[string]int64{}
		for k, val := range inCPU {
			trialCPU[k] = val
		}
		for k, val := range inMem {
			trialMem[k] = val
		}
		var plan []placement
		if ok {
			sort.Slice(normals, func(i, j int) bool {
				if normals[i].pc != normals[j].pc {
					return normals[i].pc > normals[j].pc
				}
				if normals[i].pm != normals[j].pm {
					return normals[i].pm > normals[j].pm
				}
				return normals[i].id < normals[j].id
			})
			for _, p := range normals {
				var target string
				for _, tn := range sortedNames(m.nodes) {
					if tn == v || removable[tn] {
						continue
					}
					uCPU, uMem := m.used(tn)
					if uCPU+trialCPU[tn]+p.pc <= m.nodes[m.nodeIndex(tn)].ca &&
						uMem+trialMem[tn]+p.pm <= m.nodes[m.nodeIndex(tn)].ma {
						target = tn
						break
					}
				}
				if target == "" {
					ok = false
					break
				}
				trialCPU[target] += p.pc
				trialMem[target] += p.pm
				plan = append(plan, placement{p.id, target})
			}
		}

		if ok {
			inCPU, inMem = trialCPU, trialMem
			plans[v] = plan
			removable[v] = true
			found := m.nodeIndex(v)
			if !m.nodes[found].has {
				m.nodes[found].has = true
				m.nodes[found].since = now
			}
		} else {
			found := m.nodeIndex(v)
			m.nodes[found].has = false
			m.nodes[found].since = 0
		}
	}

	res := TickResult{}
	if len(m.nodes) > m.minNodes {
		var chosen string
		var chosenSince int64
		for _, v := range sortedNames(m.nodes) {
			if !removable[v] {
				continue
			}
			n := m.nodes[m.nodeIndex(v)]
			if now-n.since < m.t {
				continue
			}
			if chosen == "" || n.since < chosenSince || (n.since == chosenSince && v < chosen) {
				chosen = v
				chosenSince = n.since
			}
		}
		if chosen != "" {
			res.Removed = chosen
			// normal 迁移：按计划更新所属节点；daemon 随节点删除。
			plan := map[string]string{}
			for _, pl := range plans[chosen] {
				plan[pl.id] = pl.target
			}
			var kept []naivePod
			for _, p := range m.pods {
				if p.node == chosen {
					if p.kind == KindNormal {
						p.node = plan[p.id]
						res.Migrations = append(res.Migrations, Migration{p.id, p.node})
						kept = append(kept, p)
					}
					continue
				}
				kept = append(kept, p)
			}
			m.pods = kept
			ci := m.nodeIndex(chosen)
			m.nodes = append(m.nodes[:ci], m.nodes[ci+1:]...)
			sort.Slice(res.Migrations, func(i, j int) bool {
				return res.Migrations[i].PodID < res.Migrations[j].PodID
			})
		}
	}

	m.seen = true
	if now > m.maxNow {
		m.maxNow = now
	}
	return res, nil
}

func (m *naiveSim) since() map[string]int64 {
	out := map[string]int64{}
	for _, n := range m.nodes {
		if n.has {
			out[n.name] = n.since
		}
	}
	return out
}

// 状态快照：节点(含 ca/ma)、Pod 归属与请求、since。
type snapshot struct {
	nodes string
	pods  string
	since string
}

func (m *naiveSim) snapshot() snapshot {
	nodeNames := sortedNames(m.nodes)
	var ns []string
	for _, n := range nodeNames {
		i := m.nodeIndex(n)
		ns = append(ns, n+":"+itoa(m.nodes[i].ca)+"/"+itoa(m.nodes[i].ma))
	}
	var lines []string
	for _, p := range m.pods {
		lines = append(lines, p.id+"@"+p.node+"#"+p.kind+":"+itoa(p.pc)+"/"+itoa(p.pm))
	}
	sort.Strings(lines)
	sincs := m.since()
	var sl []string
	for _, n := range nodeNames {
		if v, ok := sincs[n]; ok {
			sl = append(sl, n+"="+itoa(v))
		}
	}
	return snapshot{nodes: joinComma(ns), pods: joinComma(lines), since: joinComma(sl)}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}
