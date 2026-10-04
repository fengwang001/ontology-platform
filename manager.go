// Package ontology 是编排 topo/session/route 的门面。
//
// 所有变更方法持有 topo 的同一把写锁完成“判定→级联→改状态”，
// 因而并发调用的结果等价于某个串行顺序；拒绝操作绝不修改任何状态、
// 不产生事件、不占纪元。
package ontology

import (
	"ontology/route"
	"ontology/session"
	"ontology/topo"
)

// 重新导出错误，调用方只需依赖根包。
var (
	ErrInvalid    = topo.ErrInvalid
	ErrNotFound   = topo.ErrNotFound
	ErrExists     = topo.ErrExists
	ErrType       = topo.ErrType
	ErrDepth      = topo.ErrDepth
	ErrFull       = topo.ErrFull
	ErrNotBound   = topo.ErrNotBound
	ErrOffline    = topo.ErrOffline
	ErrStaleEpoch = topo.ErrStaleEpoch
	ErrBusy       = topo.ErrBusy
)

// Kind 为节点类型。
type Kind = topo.Kind

// 节点类型常量。
const (
	KindGateway = topo.KindGateway
	KindDevice  = topo.KindDevice
)

// Epoch 为上线纪元。
type Epoch = topo.Epoch

// Event 为离线事件。
type Event = session.Event

// Hop 为下行路径一跳。
type Hop = route.Hop

// Manager 是拓扑+会话+路径的统一管理器。
type Manager struct {
	topo    *topo.Topo
	session *session.Manager
	route   *route.Resolver
}

// New 创建管理器，cmax 为每网关直属孩子上限（1..100000）。
func New(cmax int) (*Manager, error) {
	t, err := topo.New(cmax)
	if err != nil {
		return nil, err
	}
	s := session.New(t)
	return &Manager{topo: t, session: s, route: route.New(t, s)}, nil
}

// AddNode(name, kind) 建立节点；重名报 ErrExists。
func (m *Manager) AddNode(name string, kind Kind) error {
	m.topo.Lock()
	defer m.topo.Unlock()
	return m.topo.AddNodeLocked(name, kind)
}

// RemoveNode 要求节点离线、未绑定且没有孩子，否则 ErrBusy。
func (m *Manager) RemoveNode(name string) error {
	m.topo.Lock()
	defer m.topo.Unlock()
	if !topo.ValidName(name) {
		return ErrInvalid
	}
	if m.session.IsOnlineLocked(name) {
		return ErrBusy
	}
	return m.topo.RemoveNodeLocked(name)
}

// Bind 把 child 绑定到网关 parent 下；child 在线时先离线级联再改绑。
func (m *Manager) Bind(child, parent string) ([]Event, error) {
	m.topo.Lock()
	defer m.topo.Unlock()
	err, noop := m.topo.CheckBindLocked(child, parent)
	if err != nil || noop {
		return nil, err
	}
	var events []Event
	if m.session.IsOnlineLocked(child) {
		events = m.session.CascadeLocked(child)
	}
	if err := m.topo.BindLocked(child, parent); err != nil {
		return nil, err
	}
	return events, nil
}

// Unbind 解绑；child 在线时先离线级联；未绑定报 ErrNotBound。
func (m *Manager) Unbind(child string) ([]Event, error) {
	m.topo.Lock()
	defer m.topo.Unlock()
	if err := m.topo.CheckUnbindLocked(child); err != nil {
		return nil, err
	}
	var events []Event
	if m.session.IsOnlineLocked(child) {
		events = m.session.CascadeLocked(child)
	}
	if err := m.topo.UnbindLocked(child); err != nil {
		return nil, err
	}
	return events, nil
}

// Online 上线或接管；返回接管时旧会话的离线事件与新纪元。
func (m *Manager) Online(node, via string, viaEpoch Epoch) ([]Event, Epoch, error) {
	m.topo.Lock()
	defer m.topo.Unlock()
	return m.session.OnlineLocked(node, via, viaEpoch)
}

// Offline 校验纪元后执行离线级联。
func (m *Manager) Offline(node string, epoch Epoch) ([]Event, error) {
	m.topo.Lock()
	defer m.topo.Unlock()
	return m.session.OfflineLocked(node, epoch)
}

// Route 返回自根网关到 node 的下行路径。
func (m *Manager) Route(node string) ([]Hop, error) {
	m.topo.RLock()
	defer m.topo.RUnlock()
	return m.route.RouteLocked(node)
}

// IsOnline 报告节点当前是否在线。
func (m *Manager) IsOnline(node string) bool {
	m.topo.RLock()
	defer m.topo.RUnlock()
	return m.session.IsOnlineLocked(node)
}

// CascadeTouched 返回最近一次顶层离线级联触碰的节点数。
func (m *Manager) CascadeTouched() int { return m.session.CascadeTouched() }

// RouteTouched 返回最近一次 Route 触碰的节点数（<=3）。
func (m *Manager) RouteTouched() int { return m.route.RouteTouched() }
