// Package api 对外门面：面追踪与欧拉公式校验的并发安全入口。依赖 face。
package api

import (
	"errors"
	"sync"

	"ontology/face"
	"ontology/pg"
)

// API 包装一个平面图及其计算结果，读写加锁，读方法可并发。
type API struct {
	mu    sync.RWMutex
	g     *pg.Graph
	faces [][]int
	comp  int
}

// New 建 n 节点空图；n 非正返回 pg.ErrNonPositiveN。
func New(n int) (*API, error) {
	g, err := pg.New(n)
	if err != nil {
		return nil, err
	}
	return &API{g: g}, nil
}

// AddEdge 加无向边；任一校验失败整体失败、状态不变。
func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v)
}

// SetRotation 设置环序；order 非邻居排列时整体失败、状态不变。
func (a *API) SetRotation(v int, order []int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.SetRotation(v, order)
}

// Compute 追踪全部面并统计连通分量。
func (a *API) Compute() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.faces = face.Trace(a.g)
	a.comp = face.Components(a.g)
}

// Faces 返回全部面（顶点序列）的副本。
func (a *API) Faces() [][]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([][]int, len(a.faces))
	for i, f := range a.faces {
		out[i] = append([]int(nil), f...)
	}
	return out
}

// FaceCount 返回面数。
func (a *API) FaceCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.faces)
}

// EdgeCount 返回无向边数。
func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

// EulerHolds 判定 V−E+F == 1+C（C 为连通分量数）。
func (a *API) EulerHolds() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.N()-a.g.EdgeCount()+len(a.faces) == 1+a.comp
}

// build 便捷构造：加边、设环序、Compute。
func build(n int, edges [][2]int, rots [][]int) *API {
	a, err := New(n)
	if err != nil {
		return nil
	}
	for _, e := range edges {
		if a.AddEdge(e[0], e[1]) != nil {
			return nil
		}
	}
	for v, r := range rots {
		if a.SetRotation(v, r) != nil {
			return nil
		}
	}
	a.Compute()
	return a
}

// SelfCheck 对一组内置嵌入核验四条不变量，全部通过才返回 true。
func SelfCheck() bool {
	ok := true
	chk := func(b bool) { ok = ok && b }
	// 嵌入1：第三节两个三角形共边，F=3，欧拉成立。
	a1 := build(4, [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {0, 3}},
		[][]int{{1, 2, 3}, {0, 3, 2}, {0, 1}, {1, 0}})
	sum := 0
	for _, f := range a1.Faces() {
		sum += len(f)
	}
	chk(a1.FaceCount() == 3 && a1.EulerHolds())
	chk(sum == 2*a1.EdgeCount())                      // 不变量1：每条边被两个面各覆盖一次
	chk(a1.FaceCount() == len(face.NaiveFaces(a1.g))) // 不变量2：与朴素参照一致
	// 嵌入2：三角形加孤立点，C=2，V−E+F=4−3+2=3=1+C，欧拉成立。
	a2 := build(4, [][2]int{{0, 1}, {1, 2}, {0, 2}}, nil)
	chk(a2.FaceCount() == 2 && a2.EulerHolds())
	chk(a2.FaceCount() == len(face.NaiveFaces(a2.g)))
	// 嵌入2b：两个不相交三角形，纯环序追踪 F=4（嵌套不在环序中），
	// V−E+F=4≠1+C=3，必须判不成立——连通公式 V−E+F=2 同样误判。
	a4 := build(6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {3, 4}, {4, 5}, {3, 5}}, nil)
	chk(a4.FaceCount() == 4 && !a4.EulerHolds())
	// 嵌入3：K4 亏格式环序，F=2，V−E+F=0≠2，欧拉必须判不成立。
	a3 := build(4, [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}},
		[][]int{{1, 2, 3}, {0, 2, 3}, {0, 1, 3}, {0, 1, 2}})
	chk(a3.FaceCount() == 2 && !a3.EulerHolds()) // 不变量3：双向判定
	chk(a3.FaceCount() == len(face.NaiveFaces(a3.g)))
	// 不变量4：四类拒绝互不相同且不留痕。
	_, e0 := New(0)
	chk(errors.Is(e0, pg.ErrNonPositiveN))
	before := a1.EdgeCount()
	chk(errors.Is(a1.AddEdge(0, 9), pg.ErrNodeOutOfRange))
	chk(errors.Is(a1.AddEdge(1, 1), pg.ErrSelfLoop))
	chk(errors.Is(a1.AddEdge(0, 1), pg.ErrDuplicateEdge))
	chk(errors.Is(a1.SetRotation(0, []int{2, 3}), pg.ErrBadRotation))
	chk(errors.Is(a1.SetRotation(0, []int{1, 1, 2}), pg.ErrBadRotation))
	chk(a1.EdgeCount() == before && a1.FaceCount() == 3)
	chk(a1.AddEdge(2, 3) == nil) // 被拒后仍可正常使用
	return ok
}
