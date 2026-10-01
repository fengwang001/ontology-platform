// Package scaledown 实现集群缩容候选判定与单次缩容执行器。
package scaledown

import (
	"errors"
	"sort"
	"sync"
)

// Pod 种类。
const (
	KindNormal = "normal"
	KindDaemon = "daemon"
	KindPinned = "pinned"
)

// 可区分的拒绝原因。
var (
	ErrInvalidConfig = errors.New("scaledown: invalid configuration")
	ErrInvalidArg    = errors.New("scaledown: invalid argument")
	ErrNodeExists    = errors.New("scaledown: node already exists")
	ErrPodExists     = errors.New("scaledown: pod already exists")
	ErrNodeNotFound  = errors.New("scaledown: node not found")
	ErrPodNotFound   = errors.New("scaledown: pod not found")
	ErrCapacity      = errors.New("scaledown: insufficient node capacity")
	ErrClockRewind   = errors.New("scaledown: clock moved backwards")
)

// Node 是节点的登记信息。
type Node struct {
	Name string
	CA   int64
	MA   int64
}

// Pod 是 Pod 的登记信息。
type Pod struct {
	ID   string
	Node string
	PC   int64
	PM   int64
	Kind string
}

// Migration 记录一次缩容中某个 Pod 迁往的目标节点。
type Migration struct {
	PodID  string
	Target string
}

// TickResult 是一轮 Tick 的执行结果。
type TickResult struct {
	Removed    string
	Migrations []Migration
}

// Scaler 是并发安全的缩容判定与执行器。
type Scaler struct {
	mu       sync.Mutex
	p        int
	t        int64
	minNodes int
	maxNow   int64
	seenTick bool
	nodes    map[string]*nodeState
	pods     map[string]*podState
}

type nodeState struct {
	ca       int64
	ma       int64
	pods     map[string]*podState
	since    int64
	hasSince bool
}

type podState struct {
	id   string
	node string
	pc   int64
	pm   int64
	kind string
}

// New 创建执行器；参数越界返回 ErrInvalidConfig。
func New(thresholdPct int, durationMs int64, minNodes int) (*Scaler, error) {
	if thresholdPct < 1 || thresholdPct > 100 || durationMs < 1 || minNodes < 0 || minNodes > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Scaler{
		p:        thresholdPct,
		t:        durationMs,
		minNodes: minNodes,
		nodes:    make(map[string]*nodeState),
		pods:     make(map[string]*podState),
	}, nil
}

// AddNode 登记节点。
func (s *Scaler) AddNode(name string, ca, ma int64) error {
	if name == "" || ca < 1 || ca > 1_000_000_000_000 || ma < 1 || ma > 1_000_000_000_000 {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[name]; ok {
		return ErrNodeExists
	}
	s.nodes[name] = &nodeState{
		ca:   ca,
		ma:   ma,
		pods: make(map[string]*podState),
	}
	return nil
}

// AddPod 登记 Pod。
func (s *Scaler) AddPod(p Pod) error {
	if p.ID == "" || !validKind(p.Kind) || p.PC < 0 || p.PC > 1_000_000_000_000 ||
		p.PM < 0 || p.PM > 1_000_000_000_000 || (p.PC == 0 && p.PM == 0) {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pods[p.ID]; ok {
		return ErrPodExists
	}
	n, ok := s.nodes[p.Node]
	if !ok {
		return ErrNodeNotFound
	}
	var usedCPU, usedMem int64
	for _, existing := range n.pods {
		usedCPU += existing.pc
		usedMem += existing.pm
	}
	if usedCPU+p.PC > n.ca || usedMem+p.PM > n.ma {
		return ErrCapacity
	}
	st := &podState{id: p.ID, node: p.Node, pc: p.PC, pm: p.PM, kind: p.Kind}
	s.pods[p.ID] = st
	n.pods[p.ID] = st
	return nil
}

// RemovePod 删除 Pod。
func (s *Scaler) RemovePod(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pods[id]
	if !ok {
		return ErrPodNotFound
	}
	delete(s.pods, id)
	delete(s.nodes[p.node].pods, id)
	return nil
}

// Tick 执行一轮判定，至多移除一个节点。
func (s *Scaler) Tick(now int64) (TickResult, error) {
	if now < 0 {
		return TickResult{}, ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seenTick && now < s.maxNow {
		return TickResult{}, ErrClockRewind
	}

	names := sortedNodeNames(s.nodes)

	// 本轮模拟迁入占用：in[target][cpu/mem]。
	incoming := make(map[string]struct{ cpu, mem int64 })
	// 本轮已判定可移除的节点。
	removableSet := make(map[string]bool)
	// 可移除节点 normal Pod 的落点：placements[node][podID] = target。
	placements := make(map[string]map[string]string)

	removableOrder := make([]string, 0)
	for _, v := range names {
		n := s.nodes[v]

		var sc, sm int64
		hasPinned := false
		var normals []*podState
		for _, p := range n.pods {
			if p.kind == KindDaemon {
				// daemon 不计入利用率，但占用节点空间。
				continue
			}
			// 非 daemon（normal 与 pinned）计入利用率。
			sc += p.pc
			sm += p.pm
			if p.kind == KindPinned {
				hasPinned = true
			} else {
				normals = append(normals, p)
			}
		}

		ok := true
		// (a) 低利用：严格小于。
		if sc*100 >= int64(s.p)*n.ca || sm*100 >= int64(s.p)*n.ma {
			ok = false
		}
		// (b) 本轮此前没有接收过任何模拟迁入。
		if ok {
			if inc := incoming[v]; inc.cpu > 0 || inc.mem > 0 {
				ok = false
			}
		}
		// (c) 没有 pinned Pod。
		if ok && hasPinned {
			ok = false
		}

		// (d) normal Pod 逐个模拟迁出。
		var trial map[string]struct{ cpu, mem int64 }
		var trialPlacements map[string]string
		if ok {
			sortPods(normals)
			trial = cloneIncoming(incoming)
			trialPlacements = make(map[string]string)
			for _, p := range normals {
				target := s.findTarget(v, p, removableSet, trial)
				if target == "" {
					ok = false
					break
				}
				inc := trial[target]
				inc.cpu += p.pc
				inc.mem += p.pm
				trial[target] = inc
				trialPlacements[p.id] = target
			}
		}

		if ok {
			// 落点保留并影响后续节点。
			incoming = trial
			placements[v] = trialPlacements
			removableSet[v] = true
			removableOrder = append(removableOrder, v)
			if !n.hasSince {
				n.hasSince = true
				n.since = now
			}
		} else {
			// 临时落点全部撤销；不可移除则清除 since。
			n.hasSince = false
			n.since = 0
		}
	}

	res := TickResult{}
	// (3) 执行：总数须大于 MinNodes。
	if len(names) > s.minNodes {
		var chosen string
		for _, v := range removableOrder {
			n := s.nodes[v]
			if now-n.since < s.t {
				continue
			}
			if chosen == "" || n.since < s.nodes[chosen].since ||
				(n.since == s.nodes[chosen].since && v < chosen) {
				chosen = v
			}
		}
		if chosen != "" {
			res = s.executeRemoval(chosen, placements[chosen])
		}
	}

	s.seenTick = true
	if now > s.maxNow {
		s.maxNow = now
	}
	return res, nil
}

// Since 返回节点持续可移除起始时刻的只读快照。
func (s *Scaler) Since() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.nodes))
	for name, n := range s.nodes {
		if n.hasSince {
			out[name] = n.since
		}
	}
	return out
}

// findTarget 按节点名字节序取第一个放得下的节点；排除 v 自身与已判定可移除节点。
func (s *Scaler) findTarget(v string, p *podState, removable map[string]bool,
	incoming map[string]struct{ cpu, mem int64 }) string {
	for _, targetName := range sortedNodeNames(s.nodes) {
		target := s.nodes[targetName]
		if targetName == v || removable[targetName] {
			continue
		}
		var usedCPU, usedMem int64
		for _, q := range target.pods {
			usedCPU += q.pc
			usedMem += q.pm
		}
		inc := incoming[targetName]
		if usedCPU+inc.cpu+p.pc <= target.ca && usedMem+inc.mem+p.pm <= target.ma {
			return targetName
		}
	}
	return ""
}

// executeRemoval 移除节点：daemon 随节点删除，normal Pod 按记录落点迁移。
func (s *Scaler) executeRemoval(name string, pm map[string]string) TickResult {
	n := s.nodes[name]
	res := TickResult{Removed: name}

	// 先把迁出 Pod 从节点摘除，再按落点加入目标节点。
	moved := make([]*podState, 0)
	for id, p := range n.pods {
		if p.kind == KindNormal {
			moved = append(moved, p)
			delete(n.pods, id)
		}
	}
	sortPods(moved)
	for _, p := range moved {
		target := pm[p.id]
		t := s.nodes[target]
		p.node = target
		t.pods[p.id] = p
		res.Migrations = append(res.Migrations, Migration{PodID: p.id, Target: target})
	}
	// daemon（及任何残余）Pod 随节点删除。
	for id := range n.pods {
		delete(s.pods, id)
	}
	delete(s.nodes, name)
	return res
}

func validKind(k string) bool {
	return k == KindNormal || k == KindDaemon || k == KindPinned
}

func sortedNodeNames(nodes map[string]*nodeState) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortPods：cpu 降序、内存降序、ID 字节序升序。
func sortPods(pods []*podState) {
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].pc != pods[j].pc {
			return pods[i].pc > pods[j].pc
		}
		if pods[i].pm != pods[j].pm {
			return pods[i].pm > pods[j].pm
		}
		return pods[i].id < pods[j].id
	})
}

func cloneIncoming(in map[string]struct{ cpu, mem int64 }) map[string]struct{ cpu, mem int64 } {
	out := make(map[string]struct{ cpu, mem int64 }, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
