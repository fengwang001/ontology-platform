// Package api 是二分图最大权完美匹配的对外入口：New/SetWeight/Solve/SelfCheck。
package api

import "errors"
import "math"
import "slices"
import "sync"
import "ontology/bmg"
import "ontology/km"

var ErrInvalidN = errors.New("api: n must be positive")
var ErrNodeOutOfRange = errors.New("api: node index out of [0,n)")
var ErrDuplicateWeight = errors.New("api: weight for (l,r) already set")
var ErrIncomplete = errors.New("api: weight matrix is incomplete")

type Matcher struct {
	mu sync.RWMutex
	g  *bmg.Graph
	s  *km.Solver
}

func New(n int) (*Matcher, error) {
	g, err := bmg.New(n)
	if err != nil {
		return nil, ErrInvalidN
	}
	return &Matcher{g: g, s: km.New()}, nil
}

func (m *Matcher) SetWeight(l, r int, w int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.g.SetWeight(l, r, w)
	switch {
	case errors.Is(err, bmg.ErrNodeOutOfRange):
		return ErrNodeOutOfRange
	case errors.Is(err, bmg.ErrDuplicateWeight):
		return ErrDuplicateWeight
	default:
		return err
	}
}
func (m *Matcher) Solve() (int64, []int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, match, err := m.s.Solve(m.g)
	if errors.Is(err, km.ErrIncomplete) {
		return 0, nil, ErrIncomplete
	}
	return v, match, err
}
func isPerfect(m []int, n int) bool {
	seen := make([]bool, n)
	for _, j := range m {
		if j < 0 || j >= n || seen[j] {
			return false
		}
		seen[j] = true
	}
	return len(m) == n
}

// bruteMax 枚举全部 n! 排列，返回最大总权与其中字典序最小匹配（n 很小）。
func bruteMax(w [][]int64) (int64, []int) {
	n := len(w)
	p, best, bestP := make([]int, n), int64(math.MinInt64), []int(nil)
	for i := range p {
		p[i] = i
	}
	var rec func(k int, sum int64)
	rec = func(k int, sum int64) {
		if k == n {
			if sum > best || sum == best && (bestP == nil || slices.Compare(p, bestP) < 0) {
				best, bestP = sum, append([]int(nil), p...)
			}
			return
		}
		for i := k; i < n; i++ {
			p[k], p[i] = p[i], p[k]
			rec(k+1, sum+w[k][p[k]])
			p[k], p[i] = p[i], p[k]
		}
	}
	rec(0, 0)
	return best, bestP
}

// SelfCheck 对内置权矩阵核验四条不变量，全部成立返回 nil。
func (m *Matcher) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cases := [][][]int64{
		{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}},
		{{7}},
		{{-5, -2}, {-3, -9}},
		{{0, 0}, {0, 0}},
		{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
		{{3, 7, 13, -11}, {-3, 19, 4, 13}, {20, -16, -16, -8}, {5, 4, 9, -20}},
	}
	for _, w := range cases {
		n := len(w)
		g, _ := bmg.New(n)
		for i, row := range w {
			for j, x := range row {
				_ = g.SetWeight(i, j, x)
			}
		}
		v, match, err := m.s.Solve(g)
		if err != nil {
			return err
		}
		if !isPerfect(match, n) { // 不变量 1
			return errors.New("selfcheck: 结果不是完美匹配")
		}
		wantV, wantM := bruteMax(w) // 不变量 2/3：最大权且与朴素枚举逐值相同（含字典序）
		if v != wantV || !slices.Equal(match, wantM) {
			return errors.New("selfcheck: 与朴素枚举结果不一致")
		}
	}
	return selfCheckRejection() // 不变量 4：失败不留痕（独立对象，不触碰接收者）
}

func selfCheckRejection() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidN) {
		return errors.New("selfcheck: n 非正未报 ErrInvalidN")
	}
	m, _ := New(2)
	for _, c := range [][2]int{{2, 0}, {0, -1}, {-1, 0}} {
		if err := m.SetWeight(c[0], c[1], 1); !errors.Is(err, ErrNodeOutOfRange) {
			return errors.New("selfcheck: 越界未报 ErrNodeOutOfRange")
		}
	}
	_ = m.SetWeight(0, 0, 1)
	if err := m.SetWeight(0, 0, 2); !errors.Is(err, ErrDuplicateWeight) {
		return errors.New("selfcheck: 重复设置未报 ErrDuplicateWeight")
	}
	if _, _, err := m.Solve(); !errors.Is(err, ErrIncomplete) {
		return errors.New("selfcheck: 未设置完全未报 ErrIncomplete")
	}
	if w, ok := m.g.Weight(0, 0); !ok || w != 1 { // 被拒写未污染已存值
		return errors.New("selfcheck: 被拒操作污染了状态")
	}
	_ = m.SetWeight(0, 1, 1) // 被拒后补全三条边，对象应仍可正常求解
	_ = m.SetWeight(1, 0, 1)
	_ = m.SetWeight(1, 1, 1)
	if _, _, err := m.Solve(); err != nil {
		return errors.New("selfcheck: 拒绝后对象不可继续使用")
	}
	return nil
}
