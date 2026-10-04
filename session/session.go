// Package session 维护上线纪元、接管与离线级联。
//
// 本包所有状态均受关联的 topo.Topo 的内部锁保护：公开方法自行加写锁，
// *Locked 原语假定调用方已持锁（供根 facade 跨包编排）。
package session

import "ontology/topo"

// Event 为一条离线事件：节点名与该节点离线时持有的原纪元。
type Event struct {
	Node  string
	Epoch topo.Epoch
}

// Manager 管理全部在线会话。
type Manager struct {
	t       *topo.Topo
	next    topo.Epoch
	online  map[string]topo.Epoch
	cascade int
}

// New 创建会话管理器。
func New(t *topo.Topo) *Manager {
	return &Manager{t: t, online: map[string]topo.Epoch{}}
}

// IsOnline 报告节点是否在线。
func (m *Manager) IsOnline(name string) bool {
	m.t.Lock()
	defer m.t.Unlock()
	return m.IsOnlineLocked(name)
}

// IsOnlineLocked 为 IsOnline 的持锁原语。
func (m *Manager) IsOnlineLocked(name string) bool {
	_, ok := m.online[name]
	return ok
}

// EpochLocked 返回节点当前纪元；不在线返回 0,false。
func (m *Manager) EpochLocked(name string) (topo.Epoch, bool) {
	e, ok := m.online[name]
	return e, ok
}

// CascadeTouched 返回最近一次顶层离线级联触碰的节点数（= 事件数）。
func (m *Manager) CascadeTouched() int {
	m.t.RLock()
	defer m.t.RUnlock()
	return m.cascade
}

// Online 按规则上线；via 为空表示直连（此时 viaEpoch 必须为 0）。
// 若 node 已在线则为接管：先离线级联（返回旧事件）再占新纪元。
func (m *Manager) Online(node, via string, viaEpoch topo.Epoch) ([]Event, topo.Epoch, error) {
	m.t.Lock()
	defer m.t.Unlock()
	return m.OnlineLocked(node, via, viaEpoch)
}

// OnlineLocked 为 Online 的持锁原语。
// 判定次序：ErrInvalid > ErrNotFound > via 与绑定不符(ErrNotBound)
// > 父节点不在线(ErrOffline) > viaEpoch 不符(ErrStaleEpoch)。
func (m *Manager) OnlineLocked(node, via string, viaEpoch topo.Epoch) ([]Event, topo.Epoch, error) {
	if !topo.ValidName(node) {
		return nil, 0, topo.ErrInvalid
	}
	if viaEpoch < 0 {
		return nil, 0, topo.ErrInvalid
	}
	direct := via == ""
	if direct && viaEpoch != 0 {
		return nil, 0, topo.ErrInvalid
	}
	if !direct && !topo.ValidName(via) {
		return nil, 0, topo.ErrInvalid
	}
	kind, ok := m.t.KindOfLocked(node)
	if !ok {
		return nil, 0, topo.ErrNotFound
	}
	parent, _ := m.t.ParentLocked(node)
	if direct {
		if !(kind == topo.KindGateway && parent == "") {
			return nil, 0, topo.ErrNotBound
		}
	} else {
		if parent != via {
			return nil, 0, topo.ErrNotBound
		}
		ep, live := m.online[via]
		if !live {
			return nil, 0, topo.ErrOffline
		}
		if ep != viaEpoch {
			return nil, 0, topo.ErrStaleEpoch
		}
	}
	var events []Event
	if _, live := m.online[node]; live {
		m.cascade = 0
		events = m.cascadeLocked(node)
	}
	m.next++
	m.online[node] = m.next
	return events, m.next, nil
}

// Offline 按纪元校验后执行离线级联。
// 判定次序：ErrNotFound（含非法名）> ErrOffline > ErrStaleEpoch。
func (m *Manager) Offline(node string, epoch topo.Epoch) ([]Event, error) {
	m.t.Lock()
	defer m.t.Unlock()
	return m.OfflineLocked(node, epoch)
}

// OfflineLocked 为 Offline 的持锁原语。
func (m *Manager) OfflineLocked(node string, epoch topo.Epoch) ([]Event, error) {
	if !topo.ValidName(node) {
		return nil, topo.ErrInvalid
	}
	if epoch < 0 {
		return nil, topo.ErrInvalid
	}
	if _, ok := m.t.KindOfLocked(node); !ok {
		return nil, topo.ErrNotFound
	}
	cur, live := m.online[node]
	if !live {
		return nil, topo.ErrOffline
	}
	if cur != epoch {
		return nil, topo.ErrStaleEpoch
	}
	m.cascade = 0
	return m.cascadeLocked(node), nil
}

// CascadeLocked 强制对 node 做离线级联（调用方已确认其在线），返回后序事件。
func (m *Manager) CascadeLocked(node string) []Event {
	m.cascade = 0
	return m.cascadeLocked(node)
}

// cascadeLocked 递归离线：先按名字节序离线每个在线直属孩子的整棵子树，
// 最后离线 node 自身；离线孩子直接跳过（不触碰、不计事件）。
func (m *Manager) cascadeLocked(node string) []Event {
	kids, _ := m.t.ChildrenLocked(node)
	var events []Event
	for _, kid := range kids {
		if _, live := m.online[kid]; !live {
			continue
		}
		events = append(events, m.cascadeLocked(kid)...)
	}
	epoch := m.online[node]
	delete(m.online, node)
	events = append(events, Event{Node: node, Epoch: epoch})
	m.cascade++
	return events
}
