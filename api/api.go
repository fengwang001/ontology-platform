// Package api 是对外面口：New/AddEdge/MinCut/EdgeCount/SelfCheck。
// 依赖 sw；读操作（MinCut/EdgeCount/SelfCheck）可并发，写操作互斥。
package api

import (
	"sync"

	"ontology/sw"
	"ontology/wg"
)

// 可判定哨兵错误，直接复用 wg 的定义。
var (
	ErrTooFewNodes       = wg.ErrTooFewNodes
	ErrNodeOutOfRange    = wg.ErrNodeOutOfRange
	ErrSelfLoop          = wg.ErrSelfLoop
	ErrDuplicateEdge     = wg.ErrDuplicateEdge
	ErrNonPositiveWeight = wg.ErrNonPositiveWeight
)

// Graph 是并发安全的加权无向图句柄。
type Graph struct {
	mu sync.RWMutex
	g  *wg.Graph
}

// New 创建 n 个节点的图；n < 2 返回 ErrTooFewNodes。
func New(n int) (*Graph, error) {
	g, err := wg.New(n)
	if err != nil {
		return nil, err
	}
	return &Graph{g: g}, nil
}

// AddEdge 登记无向边；非法入参整体失败、不改状态。
func (g *Graph) AddEdge(u, v int, w int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.g.AddEdge(u, v, w)
}

// MinCut 返回全局最小割权值。
func (g *Graph) MinCut() (int64, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return sw.MinCut(g.g), nil
}

// EdgeCount 返回已登记边数。
func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.g.EdgeCount()
}

// SelfCheck 对一组内置图核验四条不变量，全部通过返回 nil。
func (g *Graph) SelfCheck() error {
	return selfCheck()
}

func selfCheck() error {
	// 不变量 1+2：内置图（含孤立节点、需合并求和的三角）对拍朴素参照。
	cases := []struct {
		n     int
		edges [][3]int64
	}{
		{4, [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}}}, // 第三节图，答案 1
		{3, [][3]int64{{0, 1, 2}, {1, 2, 3}, {0, 2, 4}}},            // 三角，答案 5，钉住合并求和
		{5, [][3]int64{{0, 1, 7}, {2, 3, 9}}},                       // 含孤立节点，答案 0
		{2, [][3]int64{{0, 1, 42}}},                                 // 最小图
	}
	for _, c := range cases {
		g, err := New(c.n)
		if err != nil {
			return err
		}
		for _, e := range c.edges {
			if err := g.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
				return err
			}
		}
		got, err := g.MinCut()
		if err != nil {
			return err
		}
		if want := bruteForce(c.n, c.edges); got != want {
			return &MismatchError{Got: got, Want: want}
		}
	}
	// 不变量 4：四类非法操作被拒且状态不变。
	g, err := New(3)
	if err != nil {
		return err
	}
	if err := g.AddEdge(0, 1, 5); err != nil {
		return err
	}
	for _, op := range []error{
		g.AddEdge(0, 3, 1), g.AddEdge(2, 2, 1), g.AddEdge(1, 0, 9), g.AddEdge(0, 1, 0),
	} {
		if op == nil {
			return &MismatchError{Got: 0, Want: 1} // 应报错却通过
		}
	}
	if g.EdgeCount() != 1 {
		return &MismatchError{Got: int64(g.EdgeCount()), Want: 1}
	}
	if _, err := New(1); err == nil {
		return &MismatchError{Got: 0, Want: 1}
	}
	return nil
}

// MismatchError 表示自检发现值与朴素参照不一致。
type MismatchError struct{ Got, Want int64 }

func (e *MismatchError) Error() string {
	return "api: self-check mismatch"
}

// bruteForce 枚举全部二划分取最小跨边权和（朴素参照）。
func bruteForce(n int, edges [][3]int64) int64 {
	best := int64(-1)
	for mask := 1; mask < 1<<(n-1); mask++ { // 节点 n-1 固定在对侧，避免重复
		var sum int64
		for _, e := range edges {
			u, v := int(e[0]), int(e[1])
			if (mask>>u)&1 != (mask>>v)&1 {
				sum += e[2]
			}
		}
		if best < 0 || sum < best {
			best = sum
		}
	}
	return best
}
