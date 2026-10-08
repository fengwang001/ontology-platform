package heating

import (
	"sort"
	"sync"
)

// node 管网节点。
type node struct {
	id   string
	kind NodeKind
}

// valve 阀门，安装在某个管段的某一端上。
type valve struct {
	id    string
	segID string
	end   End
	state ValveState
	// closedByIsolation 标记当前关闭状态是否由某次隔离执行产生，
	// 用于修复时区分“隔离关闭的阀门”与“他人手工关闭的阀门”。
	closedByIsolation bool
}

// segment 管段，连接两个不同节点，两端各可安装零个或一个阀门。
type segment struct {
	id      string
	ends    [2]string // 两端节点 id
	valves  [2]*valve // 两端阀门，可为 nil
	leaking bool      // 是否处于泄漏（抢修）中
}

// endAt 返回管段在节点 nodeID 处的端；调用方保证 nodeID 是端点。
func (s *segment) endAt(nodeID string) End {
	if s.ends[EndA] == nodeID {
		return EndA
	}
	return EndB
}

// endClosed 报告管段某一端是否为关闭状态（关或卡死在关）。
func (s *segment) endClosed(e End) bool {
	v := s.valves[e]
	return v != nil && v.state.closed()
}

// conducts 报告管段当前是否通流：未泄漏且两端都不是关闭状态。
func (s *segment) conducts() bool {
	return !s.leaking && !s.endClosed(EndA) && !s.endClosed(EndB)
}

// isolationRecord 一次活动隔离的记录。
type isolationRecord struct {
	segID string
	// needs 为该隔离的边界上全部要求关闭的阀门（含执行前已关闭的），
	// 用于多个隔离共享边界阀门时的引用计数。
	needs map[string]struct{}
	// closed 为本次隔离实际关闭的阀门（needs 的子集）。
	closed map[string]struct{}
}

// Network 供热管网。全部公开方法可并发调用，
// 效果等价于某个串行顺序；推演读到的是一致快照。
type Network struct {
	mu       sync.RWMutex
	nodes    map[string]*node
	segments map[string]*segment
	valves   map[string]*valve
	// adj 为节点到关联管段的邻接表（指针集合，天然支持平行管段）。
	adj map[string]map[*segment]struct{}
	// isolations 以管段 id 为键的活动隔离记录。
	isolations map[string]*isolationRecord
	// hot 为当前状态下各节点是否有热，由写操作维护，
	// 使只读推演无需全网扫描即可判定停供影响。
	hot map[string]bool
}

// NewNetwork 创建空管网。
func NewNetwork() *Network {
	return &Network{
		nodes:      make(map[string]*node),
		segments:   make(map[string]*segment),
		valves:     make(map[string]*valve),
		adj:        make(map[string]map[*segment]struct{}),
		isolations: make(map[string]*isolationRecord),
		hot:        make(map[string]bool),
	}
}

// AddNode 新增节点。
func (n *Network) AddNode(id string, kind NodeKind) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id == "" || kind < NodeSource || kind > NodeUser {
		return ErrInvalidParam
	}
	if _, ok := n.nodes[id]; ok {
		return ErrNodeExists
	}
	n.nodes[id] = &node{id: id, kind: kind}
	n.adj[id] = make(map[*segment]struct{})
	n.recomputeHotLocked()
	return nil
}

// AddSegment 新增管段。两端节点必须存在且不同；两节点间允许多条管段。
func (n *Network) AddSegment(id, a, b string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id == "" {
		return ErrInvalidParam
	}
	if a == b {
		return ErrSelfLoop
	}
	if _, ok := n.segments[id]; ok {
		return ErrSegmentExists
	}
	if _, ok := n.nodes[a]; !ok {
		return ErrNodeNotFound
	}
	if _, ok := n.nodes[b]; !ok {
		return ErrNodeNotFound
	}
	s := &segment{id: id, ends: [2]string{a, b}}
	n.segments[id] = s
	n.adj[a][s] = struct{}{}
	n.adj[b][s] = struct{}{}
	n.recomputeHotLocked()
	return nil
}

// RemoveSegment 拆除管段，其端上阀门一并删除。抢修中的管段不得拆除。
func (n *Network) RemoveSegment(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, ok := n.segments[id]
	if !ok {
		return ErrSegmentNotFound
	}
	if s.leaking {
		return ErrSegmentUnderRepair
	}
	for _, e := range [2]End{EndA, EndB} {
		if v := s.valves[e]; v != nil {
			delete(n.valves, v.id)
		}
	}
	delete(n.adj[s.ends[EndA]], s)
	delete(n.adj[s.ends[EndB]], s)
	delete(n.segments, id)
	n.recomputeHotLocked()
	return nil
}

// InstallValve 在管段某端安装阀门（初始为开），每端至多一个。
func (n *Network) InstallValve(segID string, end End, valveID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if valveID == "" || (end != EndA && end != EndB) {
		return ErrInvalidParam
	}
	s, ok := n.segments[segID]
	if !ok {
		return ErrSegmentNotFound
	}
	if _, ok := n.valves[valveID]; ok {
		return ErrValveExists
	}
	if s.valves[end] != nil {
		return ErrValveExists
	}
	v := &valve{id: valveID, segID: segID, end: end, state: ValveOpen}
	s.valves[end] = v
	n.valves[valveID] = v
	n.recomputeHotLocked()
	return nil
}

// RemoveValve 拆除管段某端上的阀门。抢修中管段端上的阀门不得拆除。
func (n *Network) RemoveValve(segID string, end End) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if end != EndA && end != EndB {
		return ErrInvalidParam
	}
	s, ok := n.segments[segID]
	if !ok {
		return ErrSegmentNotFound
	}
	if s.leaking {
		return ErrSegmentUnderRepair
	}
	v := s.valves[end]
	if v == nil {
		return ErrValveNotFound
	}
	s.valves[end] = nil
	delete(n.valves, v.id)
	n.recomputeHotLocked()
	return nil
}

// OpenValve 下达开阀指令。卡死阀门不接受指令。
func (n *Network) OpenValve(valveID string) error {
	return n.commandValve(valveID, ValveOpen)
}

// CloseValve 下达关阀指令。卡死阀门不接受指令。
func (n *Network) CloseValve(valveID string) error {
	return n.commandValve(valveID, ValveClosed)
}

// commandValve 执行开关指令；已在目标状态时为空操作。
func (n *Network) commandValve(valveID string, target ValveState) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	v, ok := n.valves[valveID]
	if !ok {
		return ErrValveNotFound
	}
	if v.state.stuck() {
		return ErrValveStuck
	}
	if v.state == target {
		return nil
	}
	v.state = target
	if target == ValveOpen {
		// 一旦不再处于关闭状态，隔离关闭标记随之失效。
		v.closedByIsolation = false
	} else {
		// 手工关闭：与隔离无关，修复时不得被恢复。
		v.closedByIsolation = false
	}
	n.recomputeHotLocked()
	return nil
}

// ReportStuck 现场上报阀门卡死（卡死在开或卡死在关）。
// 卡死只能由本上报产生。
func (n *Network) ReportStuck(valveID string, stuck ValveState) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if stuck != ValveStuckOpen && stuck != ValveStuckClosed {
		return ErrInvalidParam
	}
	v, ok := n.valves[valveID]
	if !ok {
		return ErrValveNotFound
	}
	v.state = stuck
	n.recomputeHotLocked()
	return nil
}

// ConfirmValveRepaired 维修确认：卡死阀门恢复为开。
func (n *Network) ConfirmValveRepaired(valveID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	v, ok := n.valves[valveID]
	if !ok {
		return ErrValveNotFound
	}
	if !v.state.stuck() {
		return ErrValveNotStuck
	}
	v.state = ValveOpen
	v.closedByIsolation = false
	n.recomputeHotLocked()
	return nil
}

// HasHeat 报告节点当前是否有热。
func (n *Network) HasHeat(nodeID string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.hot[nodeID]
}

// ValveStateOf 查询阀门状态。
func (n *Network) ValveStateOf(valveID string) (ValveState, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	v, ok := n.valves[valveID]
	if !ok {
		return ValveOpen, ErrValveNotFound
	}
	return v.state, nil
}

// SegmentLeaking 查询管段是否处于泄漏（抢修）中。
func (n *Network) SegmentLeaking(segID string) (bool, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	s, ok := n.segments[segID]
	if !ok {
		return false, ErrSegmentNotFound
	}
	return s.leaking, nil
}

// ActiveIsolations 返回当前活动隔离的管段 id（有序）。
func (n *Network) ActiveIsolations() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]string, 0, len(n.isolations))
	for id := range n.isolations {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// recomputeHotLocked 从全部热源出发做全网 BFS，重算各节点供热状态。
// 调用方必须持有写锁。写操作的开销不在推演局部性约束范围内。
func (n *Network) recomputeHotLocked() {
	hot := make(map[string]bool, len(n.nodes))
	var queue []string
	for id, nd := range n.nodes {
		if nd.kind == NodeSource {
			hot[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for s := range n.adj[x] {
			if !s.conducts() {
				continue
			}
			y := s.ends[s.endAt(x).other()]
			if !hot[y] {
				hot[y] = true
				queue = append(queue, y)
			}
		}
	}
	n.hot = hot
}
