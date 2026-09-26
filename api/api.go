// Package api 对外接口：New/AddEdge/Solve/EdgeCount/SelfCheck，并发安全。依赖 lpath。
package api

import (
	"fmt"
	"sync"

	"ontology/lpath"
	"ontology/wdag"
)

// 对外再导出哨兵错误，判定一律用 errors.Is。
var (
	ErrBadOrder       = wdag.ErrBadOrder
	ErrNodeOutOfRange = wdag.ErrNodeOutOfRange
	ErrSelfLoop       = wdag.ErrSelfLoop
	ErrDuplicateEdge  = wdag.ErrDuplicateEdge
	ErrCycle          = wdag.ErrCycle
	ErrNoPath         = lpath.ErrNoPath
)

// API 是一个并发安全的带权 DAG 最长路径服务。
type API struct {
	mu sync.RWMutex
	g  *wdag.Graph
}

// New 建 n 个节点的实例；n 非正返回 ErrBadOrder。
func New(n int) (*API, error) {
	g, err := wdag.New(n)
	if err != nil {
		return nil, err
	}
	return &API{g: g}, nil
}

// AddEdge 加边 u→v 权 w；非法时整体失败、状态不变。
func (a *API) AddEdge(u, v int, w int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v, w)
}

// Solve 返回全局最长路径的总权与节点序列（并列取字典序最小）。
func (a *API) Solve() (int64, []int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return lpath.Solve(a.g)
}

// EdgeCount 返回已登记边数。
func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

// SelfCheck 对内置图核验四条不变量（路径合法/最长/对拍朴素/失败不留痕），不碰接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	cases := []struct {
		n     int
		edges [][3]int64
	}{
		{5, [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}}},
		{2, [][3]int64{{0, 1, -5}}}, {3, [][3]int64{{0, 1, 0}, {1, 2, 0}}}, // 全负；零权
		{4, [][3]int64{{0, 1, 1}, {1, 2, 1}, {0, 2, 2}}},   // 并列 2：[0 1 2] vs [0 2]
		{6, [][3]int64{{3, 4, 7}, {4, 5, -2}, {0, 1, 1}}},  // 多源，最长在深源
		{3, [][3]int64{{2, 0, -3}, {0, 1, 8}, {2, 1, 7}}},  // 负dist须新起路径：8 [0 1]
		{3, [][3]int64{{1, 2, -3}, {1, 0, -1}, {2, 0, 6}}}, // 中途不得重启：6 [2 0]
	}
	for i, c := range cases {
		g, err := wdag.New(c.n)
		if err != nil {
			return err
		}
		for _, e := range c.edges {
			if err := g.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
				return fmt.Errorf("selfcheck case %d build: %w", i, err)
			}
		}
		total, path, err := lpath.Solve(g)
		if err != nil {
			return fmt.Errorf("selfcheck case %d solve: %w", i, err)
		}
		if err := checkPath(g, total, path); err != nil { // 不变量1：路径合法
			return fmt.Errorf("selfcheck case %d: %w", i, err)
		}
		if bf := bruteForce(g); bf != total { // 不变量2+3：等于朴素参照
			return fmt.Errorf("selfcheck case %d: got %d want %d", i, total, bf)
		}
	}
	// 不变量4：失败不留痕，被拒后仍可正常使用。
	g, _ := wdag.New(3)
	_ = g.AddEdge(0, 1, 5)
	before := g.EdgeCount()
	for _, bad := range [][3]int64{{0, 3, 1}, {1, 1, 1}, {0, 1, 9}} {
		if err := g.AddEdge(int(bad[0]), int(bad[1]), bad[2]); err == nil {
			return fmt.Errorf("bad edge %v accepted", bad)
		}
	}
	if g.EdgeCount() != before {
		return fmt.Errorf("state changed: %d != %d", g.EdgeCount(), before)
	}
	if _, _, err := lpath.Solve(g); err != nil {
		return fmt.Errorf("unusable after rejection: %w", err)
	}
	return nil
}

// checkPath 核验 path 是真实路径且边权和等于 total。
func checkPath(g *wdag.Graph, total int64, path []int) error {
	if len(path) < 2 {
		return fmt.Errorf("path too short: %v", path)
	}
	var sum int64
	for i := 0; i+1 < len(path); i++ {
		found := false
		for _, e := range g.Out(path[i]) {
			if e.To == path[i+1] {
				sum += e.W
				found = true
			}
		}
		if !found {
			return fmt.Errorf("no edge %d->%d", path[i], path[i+1])
		}
	}
	if sum != total {
		return fmt.Errorf("weight %d != total %d", sum, total)
	}
	return nil
}

// bruteForce 枚举全部路径（至少 1 条边）取最大总权，作为朴素参照。
func bruteForce(g *wdag.Graph) int64 {
	best := int64(-1) << 62
	var dfs func(u int, sum int64, len1 bool)
	dfs = func(u int, sum int64, len1 bool) {
		if len1 && sum > best {
			best = sum
		}
		for _, e := range g.Out(u) {
			dfs(e.To, sum+e.W, true)
		}
	}
	for v := 0; v < g.N(); v++ {
		dfs(v, 0, false)
	}
	return best
}
