// Package session 管理上线纪元、接管与离线级联。
package session

import "ontology/topo"

// 复用 topo 的错误哨兵，保证三类操作错误集合一致。
var (
	ErrNotFound   = topo.ErrNotFound
	ErrType       = topo.ErrType
	ErrDepth      = topo.ErrDepth
	ErrFull       = topo.ErrFull
	ErrNotBound   = topo.ErrNotBound
	ErrOffline    = topo.ErrOffline
	ErrStaleEpoch = topo.ErrStaleEpoch
	ErrInvalid    = topo.ErrInvalid
	ErrExists     = topo.ErrExists
	ErrBusy       = topo.ErrBusy
)

// Event 与 topo.Event 同为一条离线记录。
type Event = topo.Event

// Manager 维护每个节点的在线状态与当前纪元，以及全局纪元计数。
type Manager struct {
	graph   *topo.Graph
	next    int64
	online  map[string]int64
	touched int
}

// NewManager 创建会话管理器，并向 graph 注入离线级联钩子与在线谓词。
func NewManager(graph *topo.Graph) *Manager {
	m := &Manager{graph: graph, online: make(map[string]int64)}
	graph.SetOfflineHook(m.cascadeHook)
	graph.SetOnlineCheck(m.isOnlineLocked)
	return m
}

// Graph 返回底层拓扑，便于直接执行 AddNode/Bind 等操作。
func (m *Manager) Graph() *topo.Graph { return m.graph }

func (m *Manager) cascadeHook(name string) []Event {
	if _, on := m.online[name]; !on {
		return nil
	}
	return m.cascadeLocked(name)
}

func (m *Manager) isOnlineLocked(name string) bool {
	_, ok := m.online[name]
	return ok
}

// Online 令 node 上线或接管，返回（级联事件, 新纪元, 错误）；
// 接管时先级联离线旧会话的在线后代（若有），再占新号。
func (m *Manager) Online(node string, via string, viaEpoch int64) ([]Event, int64, error) {
	if !topo.ValidName(node) || !topo.ValidNameOrEmpty(via) || viaEpoch < 0 || (via == "" && viaEpoch != 0) {
		return nil, 0, ErrInvalid
	}
	m.graph.Lock()
	defer m.graph.Unlock()

	if !m.graph.HasNode(node) {
		return nil, 0, ErrNotFound
	}
	parent, bound := m.graph.ParentOf(node)
	kind, _ := m.graph.KindOf(node)
	switch {
	case via == "" && !bound && kind != topo.KindGateway:
		// 未绑定的子设备不允许直连。
		return nil, 0, ErrNotBound
	case via == "" && bound:
		// 已绑定节点不允许直连。
		return nil, 0, ErrNotBound
	case via != "" && (!bound || parent != via):
		// 未绑定节点带 via，或 via 不是当前绑定的父节点。
		return nil, 0, ErrNotBound
	}
	if bound {
		// 父不在线先于纪元不符。
		if !m.isOnlineLocked(parent) {
			return nil, 0, ErrOffline
		}
		if cur, _ := m.EpochOfLocked(parent); cur != viaEpoch {
			return nil, 0, ErrStaleEpoch
		}
	}
	var events []Event
	if m.isOnlineLocked(node) {
		events = m.cascadeLocked(node)
	}
	// 全部校验通过后才占号，拒绝操作不留纪元洞。
	m.next++
	m.online[node] = m.next
	return events, m.next, nil
}

// Offline 以指定纪元令 node 离线，返回后序级联事件。
func (m *Manager) Offline(node string, epoch int64) ([]Event, error) {
	if !topo.ValidName(node) || epoch < 0 {
		return nil, ErrInvalid
	}
	m.graph.Lock()
	defer m.graph.Unlock()

	if !m.graph.HasNode(node) {
		return nil, ErrNotFound
	}
	cur, on := m.online[node]
	if !on {
		return nil, ErrOffline
	}
	if cur != epoch {
		return nil, ErrStaleEpoch
	}
	return m.cascadeLocked(node), nil
}

// IsOnline 报告 node 是否在线；不存在视为不在线。
func (m *Manager) IsOnline(node string) bool {
	m.graph.RLock()
	defer m.graph.RUnlock()
	return m.isOnlineLocked(node)
}

// EpochOf 返回 node 当前纪元；不在线时第二个返回值为 false。
func (m *Manager) EpochOf(node string) (int64, bool) {
	m.graph.RLock()
	defer m.graph.RUnlock()
	return m.EpochOfLocked(node)
}

// EpochOfLocked 同 EpochOf，但要求调用方已持有 Graph 的读锁或写锁。
func (m *Manager) EpochOfLocked(node string) (int64, bool) {
	ep, ok := m.online[node]
	return ep, ok
}

// cascadeLocked 令在线节点离线：后序处理在线直属孩子，最后离线自身。
func (m *Manager) cascadeLocked(name string) []Event {
	m.touched = 0
	events := make([]Event, 0)
	events = m.offlineDFS(name, events)
	return events
}

// offlineDFS 只触碰在线节点：每进入一个在线节点恰好 +1，
// 故一次级联触碰数恒等于返回事件数；离线孩子根本不被访问。
func (m *Manager) offlineDFS(name string, out []Event) []Event {
	m.touched++
	for _, child := range m.graph.SortedChildren(name) {
		if m.isOnlineLocked(child) {
			out = m.offlineDFS(child, out)
		}
	}
	ep := m.online[name]
	delete(m.online, name)
	return append(out, Event{Name: name, Epoch: ep})
}
