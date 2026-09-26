// Package api 是对外门面：New / SetWeight / Solve / SelfCheck。
// 依赖方向：api → km → bmg，禁止反向依赖。
package api

import (
	"errors"
	"slices"
	"sync"

	"ontology/bmg"
	"ontology/km"
)

var (
	ErrInvalidN   = errors.New("api: n must be positive")           // 节点数非法
	ErrIncomplete = errors.New("api: solve with incomplete matrix") // 权未设置完全
	ErrSelfCheck  = errors.New("api: self-check failed")
)

type Matcher struct {
	mu sync.RWMutex
	g  *bmg.Graph
}

func New(n int) (*Matcher, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	return &Matcher{g: bmg.New(n)}, nil
}
func (m *Matcher) SetWeight(l, r int, w int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.g.SetWeight(l, r, w) // 越界→ErrNodeRange、重复→ErrDuplicate，均不落盘
}
func (m *Matcher) Solve() (int64, []int, error) {
	m.mu.RLock() // 锁内克隆完整矩阵、锁外跑 KM，故多 goroutine 可并发
	if !m.g.Complete() {
		m.mu.RUnlock()
		return 0, nil, ErrIncomplete
	}
	c := m.g.Clone()
	m.mu.RUnlock()
	sum, mt := km.NewSolver(c).Solve()
	return sum, mt, nil
}
func brute(w [][]int64) (int64, []int) { // 枚举 n!，取最大权中字典序最小者（自检用）
	n := len(w)
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	var bs int64
	var bm []int
	first := true
	var rec func(int)
	rec = func(k int) {
		if k == n {
			var s int64
			for l, r := range p {
				s += w[l][r]
			}
			if first || s > bs || (s == bs && slices.Compare(p, bm) < 0) {
				bs, bm, first = s, append([]int(nil), p...), false
			}
			return
		}
		for i := k; i < n; i++ {
			p[k], p[i] = p[i], p[k]
			rec(k + 1)
			p[k], p[i] = p[i], p[k]
		}
	}
	rec(0)
	return bs, bm
}
func isPerm(m []int, n int) bool {
	if len(m) != n {
		return false
	}
	seen := make([]bool, n)
	for _, r := range m {
		if r < 0 || r >= n || seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}

func SelfCheck() error {
	mats := [][][]int64{
		{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}},
		{{-5, -2}, {-3, -4}},
		{{1, 1}, {1, 1}},
		{{7}},
	}
	for _, w := range mats {
		m, _ := New(len(w))
		for l := range w {
			for r, v := range w[l] {
				if e := m.SetWeight(l, r, v); e != nil {
					return ErrSelfCheck
				}
			}
		}
		sum, mt, e := m.Solve()
		bs, bm := brute(w)
		if e != nil || !isPerm(mt, len(w)) || sum != bs || !slices.Equal(mt, bm) {
			return ErrSelfCheck
		}
	}
	if !errsDistinct() || !rejectClean() || !km.VerifyDeltaBound([]int{100, 1000}, 2) {
		return ErrSelfCheck
	}
	return nil
}
func errsDistinct() bool {
	es := []error{ErrInvalidN, bmg.ErrNodeRange, bmg.ErrDuplicate, ErrIncomplete}
	for i := range es {
		for j := i + 1; j < len(es); j++ {
			if errors.Is(es[i], es[j]) {
				return false
			}
		}
	}
	_, e := New(0)
	return errors.Is(e, ErrInvalidN)
}
func rejectClean() bool {
	m, _ := New(2)
	if e := m.SetWeight(0, 0, 1); e != nil {
		return false
	}
	if !errors.Is(m.SetWeight(9, 0, 1), bmg.ErrNodeRange) ||
		!errors.Is(m.SetWeight(0, 9, 1), bmg.ErrNodeRange) ||
		!errors.Is(m.SetWeight(0, 0, 2), bmg.ErrDuplicate) {
		return false
	}
	if _, _, e := m.Solve(); !errors.Is(e, ErrIncomplete) {
		return false
	}
	for _, t := range [][3]int{{0, 1, 3}, {1, 0, 4}, {1, 1, 5}} {
		if e := m.SetWeight(t[0], t[1], int64(t[2])); e != nil {
			return false
		}
	}
	sum, mt, e := m.Solve() // [[1,3],[4,5]] 最优 [1,0]=7，状态未被污染
	return e == nil && sum == 7 && isPerm(mt, 2) && mt[0] == 1 && mt[1] == 0
}
