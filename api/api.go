// Package api 对外门面：建图、加边、计算 PEO 与弦图判定、自检。依赖 mcs。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/mcs"
	"ontology/ug"
)

// 四类可判定哨兵错误，互不相同；后三类直接复用 ug 的哨兵。
var (
	ErrInvalidN   = errors.New("api: 节点数 n 非法（必须非负）")
	ErrOutOfRange = ug.ErrOutOfRange
	ErrSelfLoop   = ug.ErrSelfLoop
	ErrDuplicate  = ug.ErrDuplicate
)

// API 是一张图的对外句柄，读写加锁，读方法可并发。
type API struct {
	mu       sync.RWMutex
	g        *ug.Graph
	peo      []int
	chordal  bool
	violator int
}

// New 建 n 个节点的空图；n<0 整体失败，n=0 是合法的空图。
func New(n int) (*API, error) {
	if n < 0 {
		return nil, ErrInvalidN
	}
	return &API{g: ug.New(n), violator: -1}, nil
}

// AddEdge 登记无向边；非法边整体失败、不改变图状态。
func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v)
}

// Compute 跑 MCS + 成团检查，缓存结果供读方法使用。
func (a *API) Compute() {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := mcs.New(a.g)
	s.Compute()
	a.peo = s.PEO()
	a.chordal = s.IsChordal()
	a.violator = s.FirstViolator()
}

// PEO 返回完美消除序的副本。
func (a *API) PEO() []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]int(nil), a.peo...)
}

// IsChordal 报告图是否弦图。
func (a *API) IsChordal() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.chordal
}

// FirstViolator 返回 PEO 中最靠前的不成团节点；弦图返回 -1。
func (a *API) FirstViolator() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.violator
}

// EdgeCount 返回已登记的无向边数。
func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

// SelfCheck 对一组内置图核验四条不变量，全部通过返回 nil。
// 表中的 peo/ok/vio 是按 MCS 规则手工推导的朴素参照结果。
func (a *API) SelfCheck() error {
	// 不变量 1、2、3：已知图的 PEO、弦图判定、首个违规节点。
	known := []struct {
		n     int
		edges [][2]int
		peo   []int
		ok    bool
		vio   int
	}{
		{6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}}, []int{5, 4, 3, 2, 1, 0}, true, -1},
		{4, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}}, []int{3, 2, 1, 0}, false, 3},                 // C4
		{4, [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}, []int{3, 2, 1, 0}, true, -1}, // K4
		{5, nil, []int{4, 3, 2, 1, 0}, true, -1},                                                   // 全孤立
		{0, nil, nil, true, -1},                                                                    // 空图
		{1, nil, []int{0}, true, -1},                                                               // 单点
	}
	for i, k := range known {
		g := ug.New(k.n)
		for _, e := range k.edges {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				return fmt.Errorf("selfcheck 建图 %d: %w", i, err)
			}
		}
		s := mcs.New(g)
		s.Compute()
		if !slices.Equal(s.PEO(), k.peo) || s.IsChordal() != k.ok || s.FirstViolator() != k.vio {
			return fmt.Errorf("selfcheck 内置图 %d 核验失败", i)
		}
	}
	// 不变量 4：被拒操作不留痕，四种错误可区分且互不相同。
	if _, err := New(-1); !errors.Is(err, ErrInvalidN) {
		return errors.New("selfcheck: 非法 n 未报 ErrInvalidN")
	}
	g := ug.New(3)
	if err := g.AddEdge(0, 1); err != nil {
		return err
	}
	rej := []error{g.AddEdge(0, 3), g.AddEdge(2, 2), g.AddEdge(1, 0)}
	if !errors.Is(rej[0], ErrOutOfRange) || !errors.Is(rej[1], ErrSelfLoop) || !errors.Is(rej[2], ErrDuplicate) {
		return errors.New("selfcheck: 非法边哨兵错误不符")
	}
	if rej[0] == rej[1] || rej[1] == rej[2] || rej[0] == rej[2] {
		return errors.New("selfcheck: 哨兵错误不互异")
	}
	if g.EdgeCount() != 1 || g.AddEdge(1, 2) != nil || g.EdgeCount() != 2 {
		return errors.New("selfcheck: 被拒操作改变了图状态")
	}
	return nil
}
