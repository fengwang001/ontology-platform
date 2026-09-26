package api

import (
	"errors"

	"ontology/face"
	"ontology/pg"
)

// buildSec3 构造第三节嵌入（V=4,E=5）并 Compute。
func buildSec3() (*API, error) {
	a, err := New(4)
	if err != nil {
		return nil, err
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {0, 3}} {
		if err = a.AddEdge(e[0], e[1]); err != nil {
			return nil, err
		}
	}
	for v, r := range [][]int{{1, 2, 3}, {0, 3, 2}, {0, 1}, {1, 0}} {
		if err = a.SetRotation(v, r); err != nil {
			return nil, err
		}
	}
	return a, a.Compute()
}

// naiveOrbits 朴素参照：每条有向边在环序切片里线性扫描定位前驱，数轨道数。
func naiveOrbits(s pg.Snapshot) int {
	vis, count := map[[2]int]struct{}{}, 0
	var walk func(u, v int)
	walk = func(u, v int) {
		if _, ok := vis[[2]int{u, v}]; ok {
			return
		}
		count++
		for {
			if _, ok := vis[[2]int{u, v}]; ok {
				return
			}
			vis[[2]int{u, v}] = struct{}{}
			r := s.Rotation[v]
			i := 0
			for r[i] != u {
				i++
			}
			u, v = v, r[(i-1+len(r))%len(r)]
		}
	}
	for _, e := range s.Edges {
		walk(e[0], e[1])
		walk(e[1], e[0])
	}
	return count
}

// SelfCheck 用内置嵌入核验四条不变量；任一不成立返回带说明的错误。
func (a *API) SelfCheck() error {
	// 不变量 1/2/3：第三节嵌入 Σ长度=2E、朴素参照逐值一致、F=3、欧拉成立。
	b, err := buildSec3()
	if err != nil {
		return err
	}
	total := 0
	for _, f := range b.Faces() {
		total += len(f)
	}
	if total != 2*b.EdgeCount() || b.FaceCount() != 3 ||
		naiveOrbits(b.snapshot()) != b.FaceCount() || !b.EulerHolds() {
		return errors.New("section3 invariants failed")
	}
	// 两个不相交三角形 V=6,E=6,F=3,C=2：6-6+3=3=1+2。
	d, _ := New(6)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {0, 2}, {3, 4}, {4, 5}, {3, 5}} {
		if err = d.AddEdge(e[0], e[1]); err != nil {
			return err
		}
	}
	for v, r := range [][]int{{1, 2}, {0, 2}, {1, 0}, {4, 5}, {3, 5}, {4, 3}} {
		if err = d.SetRotation(v, r); err != nil {
			return err
		}
	}
	if err = d.Compute(); err != nil || d.FaceCount() != 3 || !d.EulerHolds() {
		return errors.New("disjoint triangles invariants failed")
	}
	// O(1) 探针：中心度 m 多档，每次 next 定位检查邻居数恒 ≤2。
	for _, m := range []int{100, 1000, 10000} {
		if err = starProbe(m); err != nil {
			return err
		}
	}
	// 不变量 4：四类哨兵互不相同，拒绝后状态不变且仍可正常使用。
	x, _ := New(4)
	if err = x.AddEdge(0, 1); err != nil {
		return err
	}
	_, e0 := New(0)
	cases := []error{e0, x.AddEdge(0, 4), x.AddEdge(1, 1), x.AddEdge(1, 0),
		x.SetRotation(0, []int{2}), x.SetRotation(0, []int{1, 1}), x.SetRotation(0, []int{1, 2})}
	want := []error{ErrBadN, ErrBadVertex, ErrSelfLoop, ErrDuplicate, ErrBadRotation, ErrBadRotation, ErrBadRotation}
	for i := range cases {
		if !errors.Is(cases[i], want[i]) {
			return errors.New("rejection sentinel mismatch")
		}
	}
	if x.EdgeCount() != 1 {
		return errors.New("state changed after rejects")
	}
	if err = x.AddEdge(2, 3); err != nil {
		return err
	}
	for v, r := range map[int][]int{0: {1}, 1: {0}, 2: {3}, 3: {2}} {
		if err = x.SetRotation(v, r); err != nil {
			return err
		}
	}
	if err = x.Compute(); err != nil || !x.EulerHolds() {
		return errors.New("graph unusable after rejects")
	}
	return nil
}

// starProbe 构造中心连 m 叶的星形并断言 O(1) 前驱定位（不泄露探针数值）。
func starProbe(m int) error {
	g, err := pg.New(m + 1)
	if err != nil {
		return err
	}
	order := make([]int, m)
	for i := 1; i <= m; i++ {
		if err = g.AddEdge(0, i); err != nil {
			return err
		}
		order[i-1] = i
		if err = g.SetRotation(i, []int{0}); err != nil {
			return err
		}
	}
	if err = g.SetRotation(0, order); err != nil {
		return err
	}
	tr, err := face.New(g.Snapshot())
	if err != nil || !tr.PrevProbeConstant() {
		return errors.New("star probe not O(1)")
	}
	return nil
}
