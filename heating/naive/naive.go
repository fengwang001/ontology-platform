// Package naive 是供热管网隔离推演的独立朴素参考模型。
//
// 它与 heating.Network 实现同一套规格，但刻意采用最直接、显然正确
// 的写法：无锁、无缓存、无邻接表，全部通过整体扫描与前后两次全网
// BFS 得出结果。它用于随机对照测试，交叉验证优化实现的一致性。
package naive

import (
	"sort"

	"ontology/heating"
)

type nvalve struct {
	id                string
	state             heating.ValveState
	closedByIsolation bool
}

type nseg struct {
	id      string
	a, b    string
	va, vb  *nvalve
	leaking bool
}

type record struct {
	needs  map[string]bool
	closed map[string]bool
}

// Model 朴素管网模型（非并发安全）。
type Model struct {
	nodes  map[string]heating.NodeKind
	segs   map[string]*nseg
	valves map[string]*nvalve
	isol   map[string]record
}

// New 创建空模型。
func New() *Model {
	return &Model{
		nodes:  make(map[string]heating.NodeKind),
		segs:   make(map[string]*nseg),
		valves: make(map[string]*nvalve),
		isol:   make(map[string]record),
	}
}

// AddNode 新增节点。
func (m *Model) AddNode(id string, kind heating.NodeKind) error {
	if id == "" || kind < heating.NodeSource || kind > heating.NodeUser {
		return heating.ErrInvalidParam
	}
	if _, ok := m.nodes[id]; ok {
		return heating.ErrNodeExists
	}
	m.nodes[id] = kind
	return nil
}

// AddSegment 新增管段。
func (m *Model) AddSegment(id, a, b string) error {
	if id == "" {
		return heating.ErrInvalidParam
	}
	if a == b {
		return heating.ErrSelfLoop
	}
	if _, ok := m.segs[id]; ok {
		return heating.ErrSegmentExists
	}
	if _, ok := m.nodes[a]; !ok {
		return heating.ErrNodeNotFound
	}
	if _, ok := m.nodes[b]; !ok {
		return heating.ErrNodeNotFound
	}
	m.segs[id] = &nseg{id: id, a: a, b: b}
	return nil
}

// RemoveSegment 拆除管段及其端上阀门。
func (m *Model) RemoveSegment(id string) error {
	s, ok := m.segs[id]
	if !ok {
		return heating.ErrSegmentNotFound
	}
	if s.leaking {
		return heating.ErrSegmentUnderRepair
	}
	if s.va != nil {
		delete(m.valves, s.va.id)
	}
	if s.vb != nil {
		delete(m.valves, s.vb.id)
	}
	delete(m.segs, id)
	return nil
}

// InstallValve 在管段某端安装阀门。
func (m *Model) InstallValve(segID string, end heating.End, valveID string) error {
	if valveID == "" || (end != heating.EndA && end != heating.EndB) {
		return heating.ErrInvalidParam
	}
	s, ok := m.segs[segID]
	if !ok {
		return heating.ErrSegmentNotFound
	}
	if _, ok := m.valves[valveID]; ok {
		return heating.ErrValveExists
	}
	v := &nvalve{id: valveID, state: heating.ValveOpen}
	if end == heating.EndA {
		if s.va != nil {
			return heating.ErrValveExists
		}
		s.va = v
	} else {
		if s.vb != nil {
			return heating.ErrValveExists
		}
		s.vb = v
	}
	m.valves[valveID] = v
	return nil
}

// RemoveValve 拆除管段某端阀门。
func (m *Model) RemoveValve(segID string, end heating.End) error {
	if end != heating.EndA && end != heating.EndB {
		return heating.ErrInvalidParam
	}
	s, ok := m.segs[segID]
	if !ok {
		return heating.ErrSegmentNotFound
	}
	if s.leaking {
		return heating.ErrSegmentUnderRepair
	}
	var v *nvalve
	if end == heating.EndA {
		v, s.va = s.va, nil
	} else {
		v, s.vb = s.vb, nil
	}
	if v == nil {
		return heating.ErrValveNotFound
	}
	delete(m.valves, v.id)
	return nil
}

// OpenValve 开阀指令。
func (m *Model) OpenValve(valveID string) error {
	v, ok := m.valves[valveID]
	if !ok {
		return heating.ErrValveNotFound
	}
	if v.state == heating.ValveStuckOpen || v.state == heating.ValveStuckClosed {
		return heating.ErrValveStuck
	}
	if v.state != heating.ValveOpen {
		v.state = heating.ValveOpen
		v.closedByIsolation = false
	}
	return nil
}

// CloseValve 关阀指令。
func (m *Model) CloseValve(valveID string) error {
	v, ok := m.valves[valveID]
	if !ok {
		return heating.ErrValveNotFound
	}
	if v.state == heating.ValveStuckOpen || v.state == heating.ValveStuckClosed {
		return heating.ErrValveStuck
	}
	if v.state != heating.ValveClosed {
		v.state = heating.ValveClosed
		v.closedByIsolation = false
	}
	return nil
}

// ReportStuck 现场上报卡死。
func (m *Model) ReportStuck(valveID string, stuck heating.ValveState) error {
	if stuck != heating.ValveStuckOpen && stuck != heating.ValveStuckClosed {
		return heating.ErrInvalidParam
	}
	v, ok := m.valves[valveID]
	if !ok {
		return heating.ErrValveNotFound
	}
	v.state = stuck
	return nil
}

// ConfirmValveRepaired 维修确认，卡死阀门恢复为开。
func (m *Model) ConfirmValveRepaired(valveID string) error {
	v, ok := m.valves[valveID]
	if !ok {
		return heating.ErrValveNotFound
	}
	if v.state != heating.ValveStuckOpen && v.state != heating.ValveStuckClosed {
		return heating.ErrValveNotStuck
	}
	v.state = heating.ValveOpen
	v.closedByIsolation = false
	return nil
}

func closedState(s heating.ValveState) bool {
	return s == heating.ValveClosed || s == heating.ValveStuckClosed
}

// conducts 报告管段当前是否通流。
func (s *nseg) conducts() bool {
	if s.leaking {
		return false
	}
	if s.va != nil && closedState(s.va.state) {
		return false
	}
	if s.vb != nil && closedState(s.vb.state) {
		return false
	}
	return true
}

// hotNodes 从全部热源出发做全网 BFS，返回有热节点集合。
func (m *Model) hotNodes() map[string]bool {
	hot := make(map[string]bool)
	var queue []string
	for id, kind := range m.nodes {
		if kind == heating.NodeSource {
			hot[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, s := range m.segs {
			if s.a != x && s.b != x {
				continue
			}
			if !s.conducts() {
				continue
			}
			y := s.a
			if y == x {
				y = s.b
			}
			if !hot[y] {
				hot[y] = true
				queue = append(queue, y)
			}
		}
	}
	return hot
}

// HasHeat 报告节点当前是否有热。
func (m *Model) HasHeat(nodeID string) bool {
	return m.hotNodes()[nodeID]
}

// ValveStateOf 查询阀门状态。
func (m *Model) ValveStateOf(valveID string) (heating.ValveState, error) {
	v, ok := m.valves[valveID]
	if !ok {
		return heating.ValveOpen, heating.ErrValveNotFound
	}
	return v.state, nil
}

// SegmentLeaking 查询管段是否泄漏中。
func (m *Model) SegmentLeaking(segID string) (bool, error) {
	s, ok := m.segs[segID]
	if !ok {
		return false, heating.ErrSegmentNotFound
	}
	return s.leaking, nil
}

// ActiveIsolations 返回活动隔离管段 id（有序）。
func (m *Model) ActiveIsolations() []string {
	out := make([]string, 0, len(m.isol))
	for id := range m.isol {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// domainOf 以不动点迭代计算隔离域，再对最终域边界收集阀门集合（两阶段，
// 保证结果与扩展顺序无关）。
// 返回 (全部需关闭阀门, 需新关闭阀门, 隔离域 id 列表)。
func (m *Model) domainOf(segID string) (needs, toClose, domain []string, err error) {
	domainSet := map[string]bool{segID: true}
	for changed := true; changed; {
		changed = false
		for did := range domainSet {
			d := m.segs[did]
			ends := []struct {
				nodeID string
				v      *nvalve
			}{{d.a, d.va}, {d.b, d.vb}}
			for _, e := range ends {
				if e.v != nil && closedState(e.v.state) {
					continue
				}
				if m.nodes[e.nodeID] == heating.NodeSource {
					continue
				}
				for _, t := range m.segs {
					if domainSet[t.id] {
						continue
					}
					if t.a != e.nodeID && t.b != e.nodeID {
						continue
					}
					tv := t.vb
					if t.a == e.nodeID {
						tv = t.va
					}
					if tv == nil || tv.state == heating.ValveStuckOpen {
						if !domainSet[t.id] {
							domainSet[t.id] = true
							changed = true
						}
					}
				}
			}
		}
	}
	// 第二阶段：对最终隔离域的边界收集需关闭的阀门。
	needSet := make(map[string]bool)
	for did := range domainSet {
		d := m.segs[did]
		ends := []struct {
			nodeID string
			v      *nvalve
		}{{d.a, d.va}, {d.b, d.vb}}
		for _, e := range ends {
			if e.v != nil && closedState(e.v.state) {
				continue
			}
			if m.nodes[e.nodeID] == heating.NodeSource {
				if e.v == nil || e.v.state == heating.ValveStuckOpen {
					return nil, nil, nil, heating.ErrNotIsolatable
				}
				needSet[e.v.id] = true
				continue
			}
			for _, t := range m.segs {
				if domainSet[t.id] {
					continue
				}
				if t.a != e.nodeID && t.b != e.nodeID {
					continue
				}
				tv := t.vb
				if t.a == e.nodeID {
					tv = t.va
				}
				needSet[tv.id] = true
			}
		}
	}
	for id := range needSet {
		needs = append(needs, id)
		if m.valves[id].state == heating.ValveOpen {
			toClose = append(toClose, id)
		}
	}
	for id := range domainSet {
		domain = append(domain, id)
	}
	sort.Strings(needs)
	sort.Strings(toClose)
	sort.Strings(domain)
	return needs, toClose, domain, nil
}

// affectedUsers 通过前后两次全网 BFS 求失去供热的用户入口。
func (m *Model) affectedUsers(segID string, toClose []string) []string {
	before := m.hotNodes()
	// 应用假想状态。
	saved := make(map[string]heating.ValveState, len(toClose))
	for _, id := range toClose {
		saved[id] = m.valves[id].state
		m.valves[id].state = heating.ValveClosed
	}
	s := m.segs[segID]
	wasLeaking := s.leaking
	s.leaking = true
	after := m.hotNodes()
	// 还原。
	for id, st := range saved {
		m.valves[id].state = st
	}
	s.leaking = wasLeaking

	var out []string
	for id, kind := range m.nodes {
		if kind == heating.NodeUser && before[id] && !after[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Simulate 只读推演。
func (m *Model) Simulate(segID string) (*heating.Plan, error) {
	if segID == "" {
		return nil, heating.ErrInvalidParam
	}
	if _, ok := m.segs[segID]; !ok {
		return nil, heating.ErrSegmentNotFound
	}
	_, toClose, domain, err := m.domainOf(segID)
	if err != nil {
		return nil, err
	}
	return &heating.Plan{
		Segment:       segID,
		ValvesToClose: toClose,
		Domain:        domain,
		AffectedUsers: m.affectedUsers(segID, toClose),
	}, nil
}

// Execute 执行隔离（全有或全无）。
func (m *Model) Execute(segID string, expected []string) (*heating.Plan, error) {
	if segID == "" {
		return nil, heating.ErrInvalidParam
	}
	if _, ok := m.segs[segID]; !ok {
		return nil, heating.ErrSegmentNotFound
	}
	if _, ok := m.isol[segID]; ok {
		return nil, heating.ErrSegmentIsolated
	}
	needs, toClose, domain, err := m.domainOf(segID)
	if err != nil {
		return nil, err
	}
	if expected != nil && !sameSet(toClose, expected) {
		return nil, heating.ErrValveStateChanged
	}
	// 影响必须在变更前基于当前状态计算。
	affected := m.affectedUsers(segID, toClose)
	closed := make(map[string]bool, len(toClose))
	for _, id := range toClose {
		v := m.valves[id]
		v.state = heating.ValveClosed
		v.closedByIsolation = true
		closed[id] = true
	}
	needSet := make(map[string]bool, len(needs))
	for _, id := range needs {
		needSet[id] = true
	}
	m.segs[segID].leaking = true
	m.isol[segID] = record{needs: needSet, closed: closed}
	return &heating.Plan{
		Segment:       segID,
		ValvesToClose: toClose,
		Domain:        domain,
		AffectedUsers: affected,
	}, nil
}

// Repair 修复完成。
func (m *Model) Repair(segID string) error {
	if segID == "" {
		return heating.ErrInvalidParam
	}
	if _, ok := m.segs[segID]; !ok {
		return heating.ErrSegmentNotFound
	}
	rec, ok := m.isol[segID]
	if !ok {
		return heating.ErrSegmentNotIsolated
	}
	m.segs[segID].leaking = false
	delete(m.isol, segID)
	stillNeeded := make(map[string]bool)
	for _, other := range m.isol {
		for id := range other.needs {
			stillNeeded[id] = true
		}
	}
	for id := range rec.needs {
		if stillNeeded[id] {
			continue
		}
		v := m.valves[id]
		if v == nil {
			continue
		}
		if v.state == heating.ValveClosed && v.closedByIsolation {
			v.state = heating.ValveOpen
			v.closedByIsolation = false
		}
	}
	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	bb := append([]string(nil), b...)
	sort.Strings(bb)
	for i := range a {
		if a[i] != bb[i] {
			return false
		}
	}
	return true
}
