// Package api 是平面嵌入面追踪的对外入口：建图、设环序、Compute 后读取
// 全部面 / 面数 / 欧拉结论；SelfCheck 用内置嵌入核验四条不变量。
package api

import (
	"sync"

	"ontology/face"
	"ontology/pg"
)

// 四类可判定哨兵错误，互不相同（直接复用 pg 的判定）。
var (
	ErrBadN        = pg.ErrBadN
	ErrBadVertex   = pg.ErrBadVertex
	ErrSelfLoop    = pg.ErrSelfLoop
	ErrDuplicate   = pg.ErrDuplicateEdge
	ErrBadRotation = pg.ErrBadRotation
)

// API 持有图与最近一次 Compute 的不可变追踪结果；所有方法可并发调用。
type API struct {
	mu sync.RWMutex
	g  *pg.Graph
	tr *face.Tracker
	ok bool
}

// New 固定节点数 n（n 非正返回 ErrBadN）。
func New(n int) (*API, error) {
	g, err := pg.New(n)
	if err != nil {
		return nil, err
	}
	return &API{g: g}, nil
}

// AddEdge 加无向简单边；被拒时状态不变，既有 Compute 结果仍可用。
func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.g.AddEdge(u, v); err != nil {
		return err
	}
	a.tr, a.ok = nil, false // 只有成功才令旧结果失效
	return nil
}

// SetRotation 指定 v 的环序；非法排列返回 ErrBadRotation 且状态不变。
func (a *API) SetRotation(v int, order []int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.g.SetRotation(v, order); err != nil {
		return err
	}
	a.tr, a.ok = nil, false
	return nil
}

// Compute 追踪全部面并固定结果；非孤立节点缺环序返回 face.ErrIncompleteRotation。
func (a *API) Compute() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	tr, err := face.New(a.g.Snapshot())
	if err != nil {
		return err
	}
	a.tr, a.ok = tr, true
	return nil
}

// Faces 返回最近一次 Compute 的全部面（防御性拷贝）；未 Compute 时为 nil。
func (a *API) Faces() [][]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.ok {
		return nil
	}
	return a.tr.Faces()
}

func (a *API) FaceCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.ok {
		return 0
	}
	return a.tr.FaceCount()
}

func (a *API) EulerHolds() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ok && a.tr.EulerHolds()
}

// EdgeCount 返回边数（Compute 之后与追踪结果一致）。
func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.ok {
		return a.tr.EdgeCount()
	}
	return len(a.g.Snapshot().Edges)
}

// snapshot 供同包 SelfCheck 在锁内取图快照。
func (a *API) snapshot() pg.Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.Snapshot()
}
