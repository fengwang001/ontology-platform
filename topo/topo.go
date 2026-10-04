// Package topo 维护边缘网关/子设备的节点、绑定关系、层级与容量约束。
package topo

import (
	"errors"
	"sort"
	"sync"
)

// Epoch 是全局递增的上线纪元。
type Epoch = int64

// Kind 为节点类型。
type Kind int

const (
	// KindUnknown 为零值，非法。
	KindUnknown Kind = iota
	// KindGateway 网关：可直连平台，也可绑定在另一网关之下。
	KindGateway
	// KindDevice 子设备：必须绑定在网关之下。
	KindDevice
)

var (
	// ErrInvalid 参数非法（名字、kind、纪元、viaEpoch 等）。
	ErrInvalid = errors.New("topo: invalid argument")
	// ErrNotFound 节点不存在。
	ErrNotFound = errors.New("topo: node not found")
	// ErrExists 重名。
	ErrExists = errors.New("topo: node already exists")
	// ErrType 类型不符（parent 不是网关，或 child==parent）。
	ErrType = errors.New("topo: type mismatch")
	// ErrDepth 网关层级超限。
	ErrDepth = errors.New("topo: gateway depth exceeded")
	// ErrFull 直属孩子达到 Cmax。
	ErrFull = errors.New("topo: children capacity full")
	// ErrNotBound 节点未按要求绑定。
	ErrNotBound = errors.New("topo: not bound as required")
	// ErrOffline 节点不在线，或父节点不在线。
	ErrOffline = errors.New("topo: node offline")
	// ErrStaleEpoch 纪元与当前纪元不符。
	ErrStaleEpoch = errors.New("topo: stale epoch")
	// ErrBusy 节点仍在线/仍有绑定关系，不能删除。
	ErrBusy = errors.New("topo: node busy")
)

type node struct {
	name     string
	kind     Kind
	parent   string
	children map[string]struct{}
}

// Topo 是拓扑管理器；其内部锁供 session/route 及上层 facade 复用。
type Topo struct {
	mu   sync.RWMutex
	cmax int
	node map[string]*node
}

// New 创建拓扑，cmax 为每个网关的直属孩子上限（1..100000）。
func New(cmax int) (*Topo, error) {
	if cmax < 1 || cmax > 100000 {
		return nil, ErrInvalid
	}
	return &Topo{cmax: cmax, node: map[string]*node{}}, nil
}

// MaxChildren 返回直属孩子上限。
func (t *Topo) MaxChildren() int { return t.cmax }

// Lock 加写锁（facade 编排跨包状态时使用）。
func (t *Topo) Lock() { t.mu.Lock() }

// Unlock 解写锁。
func (t *Topo) Unlock() { t.mu.Unlock() }

// RLock 加读锁。
func (t *Topo) RLock() { t.mu.RLock() }

// RUnlock 解读锁。
func (t *Topo) RUnlock() { t.mu.RUnlock() }

// ValidName 报告节点名是否合法：1..64 字节非空字节串。
func ValidName(name string) bool { return len(name) >= 1 && len(name) <= 64 }

// ValidKind 报告 kind 是否合法。
func ValidKind(kind Kind) bool { return kind == KindGateway || kind == KindDevice }

// AddNode 建立节点。
func (t *Topo) AddNode(name string, kind Kind) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.AddNodeLocked(name, kind)
}

// AddNodeLocked 为 AddNode 的持锁原语。
func (t *Topo) AddNodeLocked(name string, kind Kind) error {
	if !ValidName(name) || !ValidKind(kind) {
		return ErrInvalid
	}
	if _, ok := t.node[name]; ok {
		return ErrExists
	}
	t.node[name] = &node{name: name, kind: kind, children: map[string]struct{}{}}
	return nil
}

// RemoveNode 删除节点：要求离线、未绑定、无孩子。
func (t *Topo) RemoveNode(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.RemoveNodeLocked(name)
}

// RemoveNodeLocked 为 RemoveNode 的持锁原语；调用方须已先确认节点离线。
func (t *Topo) RemoveNodeLocked(name string) error {
	n, ok := t.node[name]
	if !ok {
		return ErrNotFound
	}
	if n.parent != "" || len(n.children) > 0 {
		return ErrBusy
	}
	delete(t.node, name)
	return nil
}

// Bind 把 child 绑定到网关 parent 之下。
func (t *Topo) Bind(child, parent string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.BindLocked(child, parent)
}

// BindLocked 为 Bind 的持锁原语；调用方须已先完成在线节点的离线级联。
// 判定次序：非法参数 > child/parent 不存在 > ErrType > 已绑同一父（空操作）
// > ErrDepth > ErrFull。
func (t *Topo) BindLocked(child, parent string) error {
	err, noop := t.CheckBindLocked(child, parent)
	if err != nil || noop {
		return err
	}
	t.commitBind(child, parent)
	return nil
}

// CheckBindLocked 只做 Bind 的前置判定，不修改状态。
// 返回 noop=true 表示 child 已绑定在 parent 下（空操作，err 为 nil）。
func (t *Topo) CheckBindLocked(child, parent string) (err error, noop bool) {
	if !ValidName(child) || !ValidName(parent) {
		return ErrInvalid, false
	}
	c, ok := t.node[child]
	if !ok {
		return ErrNotFound, false
	}
	p, ok := t.node[parent]
	if !ok {
		return ErrNotFound, false
	}
	if p.kind != KindGateway || child == parent {
		return ErrType, false
	}
	if c.parent == parent {
		return nil, true
	}
	if c.kind == KindGateway && (p.parent != "" || t.hasGatewayChild(c)) {
		return ErrDepth, false
	}
	if len(p.children) >= t.cmax {
		return ErrFull, false
	}
	return nil, false
}

// hasGatewayChild 报告网关 n 是否已有直属子网关（子设备不计层级）。
func (t *Topo) hasGatewayChild(n *node) bool {
	for ch := range n.children {
		if t.node[ch].kind == KindGateway {
			return true
		}
	}
	return false
}

// commitBind 执行改绑；调用方须已通过 CheckBindLocked 且非空操作。
func (t *Topo) commitBind(child, parent string) {
	c := t.node[child]
	p := t.node[parent]
	if c.parent != "" {
		delete(t.node[c.parent].children, child)
	}
	c.parent = parent
	p.children[child] = struct{}{}
}

// Unbind 解除 child 的绑定。
func (t *Topo) Unbind(child string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.UnbindLocked(child)
}

// UnbindLocked 为 Unbind 的持锁原语；调用方须已先完成离线级联。
func (t *Topo) UnbindLocked(child string) error {
	if err := t.CheckUnbindLocked(child); err != nil {
		return err
	}
	c := t.node[child]
	delete(t.node[c.parent].children, child)
	c.parent = ""
	return nil
}

// CheckUnbindLocked 只做 Unbind 的前置判定，不修改状态。
func (t *Topo) CheckUnbindLocked(child string) error {
	if !ValidName(child) {
		return ErrInvalid
	}
	c, ok := t.node[child]
	if !ok {
		return ErrNotFound
	}
	if c.parent == "" {
		return ErrNotBound
	}
	return nil
}

// KindOf 返回节点类型与是否存在。
func (t *Topo) KindOf(name string) (Kind, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.KindOfLocked(name)
}

// KindOfLocked 为 KindOf 的持锁原语。
func (t *Topo) KindOfLocked(name string) (Kind, bool) {
	n, ok := t.node[name]
	if !ok {
		return KindUnknown, false
	}
	return n.kind, true
}

// Parent 返回绑定父节点；未绑定返回空串；节点不存在也返回空串,false。
func (t *Topo) Parent(name string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ParentLocked(name)
}

// ParentLocked 为 Parent 的持锁原语。
func (t *Topo) ParentLocked(name string) (string, bool) {
	n, ok := t.node[name]
	if !ok {
		return "", false
	}
	return n.parent, true
}

// ChildrenLocked 返回按字节序排列的直属孩子；name 不存在返回 nil,false。
func (t *Topo) ChildrenLocked(name string) ([]string, bool) {
	n, ok := t.node[name]
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(n.children))
	for ch := range n.children {
		out = append(out, ch)
	}
	sort.Strings(out)
	return out, true
}

// ChildCountLocked 返回直属孩子数；name 不存在返回 0,false。
func (t *Topo) ChildCountLocked(name string) (int, bool) {
	n, ok := t.node[name]
	if !ok {
		return 0, false
	}
	return len(n.children), true
}
