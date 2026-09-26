// Package api 是一致性哈希环的对外接口：节点增删、键归属查询、自检。
// 依赖 ring。所有方法可并发调用（内部互斥锁保护）。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hashk"
	"ontology/ring"
)

// 四个互不相同的哨兵错误，可用 errors.Is 判定。
var (
	ErrEmptyRing     = errors.New("ring is empty")
	ErrNodeExists    = errors.New("node already exists")
	ErrNodeMissing   = errors.New("node does not exist")
	ErrInvalidVnodes = errors.New("vnodes must be >= 1")
)

// API 是对外的一致性哈希环。
type API struct {
	mu sync.Mutex
	r  *ring.Ring
}

// New 创建环；vnodes < 1 时失败且不留任何状态。
func New(vnodes int) (*API, error) {
	if vnodes < 1 {
		return nil, ErrInvalidVnodes
	}
	return &API{r: ring.New(vnodes)}, nil
}

// AddNode 挂入节点；重复挂入被拒绝且状态不变。
func (a *API) AddNode(id uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.r.AddNode(id) {
		return ErrNodeExists
	}
	return nil
}

// RemoveNode 摘除节点；节点不存在被拒绝且状态不变。
func (a *API) RemoveNode(id uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.r.RemoveNode(id) {
		return ErrNodeMissing
	}
	return nil
}

// Get 返回键的归属节点；环空时返回 ErrEmptyRing。
func (a *API) Get(key uint32) (uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	node, ok := a.r.Successor(hashk.KeyPos(key))
	if !ok {
		return 0, ErrEmptyRing
	}
	return node, nil
}

// NodeCount 返回当前节点数。
func (a *API) NodeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.r.NodeCount()
}

// SelfCheck 用内置节点与键核验四条不变量；全部通过返回 nil。
// 只在临时环上操作，不读取也不影响接收者状态。
func (a *API) SelfCheck() error {
	tmp, err := New(2)
	if err != nil {
		return err
	}
	for _, id := range []uint32{1, 2, 3} {
		if err := tmp.AddNode(id); err != nil {
			return fmt.Errorf("selfcheck setup: %w", err)
		}
	}
	// I1+I2：一组内置键的 Get 必成功且等于朴素参照（排序后线性扫描含回绕）。
	snap := tmp.snapshot()
	for k := uint32(0); k < 256; k++ {
		got, err := tmp.Get(k)
		if err != nil {
			return fmt.Errorf("selfcheck I1: key %d: %w", k, err)
		}
		if want := naive(snap, hashk.KeyPos(k)); got != want {
			return fmt.Errorf("selfcheck I2: key %d: got %d want %d", k, got, want)
		}
	}
	// I3：RemoveNode(3) 后任何键不再归 3。
	if err := tmp.RemoveNode(3); err != nil {
		return fmt.Errorf("selfcheck I3 setup: %w", err)
	}
	snap = tmp.snapshot()
	for k := uint32(0); k < 256; k++ {
		got, err := tmp.Get(k)
		if err != nil || got == 3 {
			return fmt.Errorf("selfcheck I3: key %d -> %d, %v", k, got, err)
		}
		if want := naive(snap, hashk.KeyPos(k)); got != want {
			return fmt.Errorf("selfcheck I2 after remove: key %d: got %d want %d", k, got, want)
		}
	}
	// I4：四类拒绝互不相同且不留痕。
	before := tmp.NodeCount()
	if err := tmp.AddNode(1); !errors.Is(err, ErrNodeExists) {
		return fmt.Errorf("selfcheck I4: dup add: %v", err)
	}
	if err := tmp.RemoveNode(99); !errors.Is(err, ErrNodeMissing) {
		return fmt.Errorf("selfcheck I4: missing remove: %v", err)
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidVnodes) {
		return fmt.Errorf("selfcheck I4: bad vnodes: %v", err)
	}
	empty, _ := New(1)
	if _, err := empty.Get(0); !errors.Is(err, ErrEmptyRing) {
		return fmt.Errorf("selfcheck I4: empty get: %v", err)
	}
	if tmp.NodeCount() != before {
		return fmt.Errorf("selfcheck I4: state changed after rejections")
	}
	return nil
}

// snapshot 返回有序虚节点副本（包内自用）。
func (a *API) snapshot() []ring.VNode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.r.Snapshot()
}

// naive 是朴素参照：在有序虚节点列表里线性找首个 Pos >= keyPos，全无则回绕取 [0]。
func naive(snap []ring.VNode, keyPos uint32) uint32 {
	for _, vn := range snap {
		if vn.Pos >= keyPos {
			return vn.Node
		}
	}
	return snap[0].Node
}
