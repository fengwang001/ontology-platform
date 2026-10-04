// Package topo 维护边缘网关与子设备的绑定关系、层级与容量约束。
package topo

import (
	"errors"
	"sort"
	"sync"
	"unicode/utf8"
)

var (
	ErrNotFound   = errors.New("topo: node not found")
	ErrType       = errors.New("topo: node kind mismatch")
	ErrDepth      = errors.New("topo: gateway depth limit exceeded")
	ErrFull       = errors.New("topo: parent children capacity reached")
	ErrNotBound   = errors.New("topo: node is not bound as required")
	ErrOffline    = errors.New("topo: node is offline")
	ErrStaleEpoch = errors.New("topo: stale epoch")
	ErrInvalid    = errors.New("topo: invalid argument")
	ErrExists     = errors.New("topo: node already exists")
	ErrBusy       = errors.New("topo: node is busy")
)

// Kind 为节点类型。
type Kind int

const (
	KindUnknown Kind = iota
	KindGateway
	KindDevice
)

// Event 为一次离线产生的事件：节点名与其离线前持有的纪元。
type Event struct {
	Name  string
	Epoch int64
}

// OfflineHook 在拓扑操作需要令某在线节点（及其在线后代）离线时被调用，
// 调用时已持有 Graph 的写锁，返回按后序排列的离线事件。
type OfflineHook func(name string) []Event

// Graph 是线程安全的拓扑图，同时是整个管理器唯一的锁。
type Graph struct {
	mu          sync.RWMutex
	cMax        int
	hook        OfflineHook
	onlineCheck func(name string) bool
	nodes       map[string]*node
}

type node struct {
	name     string
	kind     Kind
	parent   string
	children map[string]struct{}
}

// NewGraph 创建拓扑。cMax 为每个网关直属孩子上限（1..100000）。
func NewGraph(cMax int) (*Graph, error) {
	if cMax < 1 || cMax > 100000 {
		return nil, ErrInvalid
	}
	return &Graph{cMax: cMax, nodes: make(map[string]*node)}, nil
}

// SetOfflineHook 注入离线级联钩子，须在任何拓扑操作前调用一次。
func (g *Graph) SetOfflineHook(h OfflineHook) { g.hook = h }

// SetOnlineCheck 注入在线谓词（只读、无副作用），供 RemoveNode 判定 ErrBusy。
func (g *Graph) SetOnlineCheck(f func(name string) bool) { g.onlineCheck = f }

// Lock / Unlock 导出内部写锁，供 session、route 在复合操作期间串行化。
// 会话与拓扑只共享这一把锁，因此复合操作（校验+改态+级联）原子可见。
func (g *Graph) Lock()   { g.mu.Lock() }
func (g *Graph) Unlock() { g.mu.Unlock() }

// RLock / RUnlock 供只读路径解析使用。
func (g *Graph) RLock()   { g.mu.RLock() }
func (g *Graph) RUnlock() { g.mu.RUnlock() }

// ValidName 校验节点名：1..64 字节的非空字节串（必须为合法 UTF-8）。
func ValidName(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	return utf8.ValidString(name)
}

// ValidNameOrEmpty 允许空 via（表示直连）。
func ValidNameOrEmpty(name string) bool { return name == "" || ValidName(name) }

func (g *Graph) addNodeLocked(name string, kind Kind) error {
	if !ValidName(name) || (kind != KindGateway && kind != KindDevice) {
		return ErrInvalid
	}
	if _, ok := g.nodes[name]; ok {
		return ErrExists
	}
	g.nodes[name] = &node{name: name, kind: kind, children: make(map[string]struct{})}
	return nil
}

// AddNode 建立节点。
func (g *Graph) AddNode(name string, kind Kind) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addNodeLocked(name, kind)
}

func (g *Graph) removeNodeLocked(name string) error {
	if !ValidName(name) {
		return ErrInvalid
	}
	n, ok := g.nodes[name]
	if !ok {
		return ErrNotFound
	}
	if n.parent != "" || len(n.children) > 0 {
		return ErrBusy
	}
	delete(g.nodes, name)
	return nil
}

// RemoveNode 删除离线、未绑定且无孩子的节点。
// 离线条件由钩子查询会话；未注入钩子时不检查在线状态（仅用于独立使用拓扑）。
func (g *Graph) RemoveNode(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.onlineCheck != nil && g.onlineCheck(name) {
		return ErrBusy
	}
	return g.removeNodeLocked(name)
}

// bindLocked 要求已持有写锁。返回（离线事件, 是否错误）。
func (g *Graph) bindLocked(child, parent string) ([]Event, error) {
	if !ValidName(child) || !ValidName(parent) {
		return nil, ErrInvalid
	}
	c, ok := g.nodes[child]
	if !ok {
		return nil, ErrNotFound
	}
	p, pok := g.nodes[parent]
	if !pok || p.kind != KindGateway || child == parent {
		return nil, ErrType
	}
	// 层级超限先于容量；网关最多两层。
	if c.kind == KindGateway {
		if p.parent != "" {
			return nil, ErrDepth
		}
		if len(c.children) > 0 {
			var hasSubGateway bool
			for ch := range c.children {
				if g.nodes[ch].kind == KindGateway {
					hasSubGateway = true
					break
				}
			}
			if hasSubGateway {
				return nil, ErrDepth
			}
		}
	}
	// 已绑定在同一父下：空操作，先于容量判定，不产生事件。
	if c.parent == parent {
		return nil, nil
	}
	if len(p.children) >= g.cMax {
		return nil, ErrFull
	}
	var events []Event
	if g.hook != nil {
		events = g.hook(child)
	}
	if c.parent != "" {
		delete(g.nodes[c.parent].children, child)
	}
	c.parent = parent
	p.children[child] = struct{}{}
	return events, nil
}

// Bind 把 child 绑定到网关 parent 下；在线 child 先级联离线（事件经返回值带出）。
func (g *Graph) Bind(child, parent string) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bindLocked(child, parent)
}

func (g *Graph) unbindLocked(child string) ([]Event, error) {
	if !ValidName(child) {
		return nil, ErrInvalid
	}
	c, ok := g.nodes[child]
	if !ok {
		return nil, ErrNotFound
	}
	if c.parent == "" {
		return nil, ErrNotBound
	}
	var events []Event
	if g.hook != nil {
		events = g.hook(child)
	}
	delete(g.nodes[c.parent].children, child)
	c.parent = ""
	return events, nil
}

// Unbind 解绑 child；在线 child 先级联离线。
func (g *Graph) Unbind(child string) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.unbindLocked(child)
}

// BindLocked / UnbindLocked 供会话管理器在已持有同一把锁时调用。
func (g *Graph) BindLocked(child, parent string) ([]Event, error) {
	return g.bindLocked(child, parent)
}

func (g *Graph) UnbindLocked(child string) ([]Event, error) {
	return g.unbindLocked(child)
}

// HasNode 报告节点是否存在。
// 不自行加锁：调用方须已持有 Graph 的读锁或写锁。
func (g *Graph) HasNode(name string) bool {
	_, ok := g.nodes[name]
	return ok
}

// KindOf 返回节点类型。
func (g *Graph) KindOf(name string) (Kind, bool) {
	n, ok := g.nodes[name]
	if !ok {
		return KindUnknown, false
	}
	return n.kind, true
}

// ParentOf 返回当前绑定的父节点；未绑定返回空串与 false。
func (g *Graph) ParentOf(name string) (string, bool) {
	n, ok := g.nodes[name]
	if !ok || n.parent == "" {
		return "", false
	}
	return n.parent, true
}

// SortedChildren 返回按名字节序排列的直属孩子。
func (g *Graph) SortedChildren(name string) []string {
	n, ok := g.nodes[name]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(n.children))
	for ch := range n.children {
		out = append(out, ch)
	}
	sort.Strings(out)
	return out
}

// ChildCount 返回直属孩子数。
func (g *Graph) ChildCount(name string) int {
	n, ok := g.nodes[name]
	if !ok {
		return 0
	}
	return len(n.children)
}
