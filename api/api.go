// Package api 对外提供 DAG 全局最长路径接口。依赖 lpath。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/lpath"
	"ontology/wdag"
)

// Graph 是对外句柄，内部持有一张 wdag.Graph。
// EdgeCount、Solve、SelfCheck 可被多 goroutine 并发调用。
type Graph struct {
	mu sync.RWMutex
	g  *wdag.Graph
}

// New 建 n 个节点的图；n 非正返回 wdag.ErrBadN。
func New(n int) (*Graph, error) {
	g, err := wdag.New(n)
	if err != nil {
		return nil, err
	}
	return &Graph{g: g}, nil
}

// AddEdge 加边 u→v 权 w；非法边整体失败且不改状态。
func (a *Graph) AddEdge(u, v int, w int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v, w)
}

// EdgeCount 返回已登记边数。
func (a *Graph) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

// Solve 返回全局最长路径的总权与节点序列（并列取字典序最小）。
func (a *Graph) Solve() (int64, []int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return lpath.New(a.g).Solve()
}

// SelfCheck 对一组内置图核验四条不变量，全部通过返回 nil。
func (a *Graph) SelfCheck() error {
	cases := []struct {
		n     int
		edges [][3]int64
	}{
		{5, [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}}},
		{2, [][3]int64{{0, 1, -5}}}, // 全负权：最长为 -5
		{7, [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}, {5, 6, 100}}}, // 多源
		{4, [][3]int64{{0, 1, 0}, {1, 2, 0}, {0, 2, 0}}},                                                          // 零权并列
	}
	for i, c := range cases {
		g, err := New(c.n)
		if err != nil {
			return err
		}
		for _, e := range c.edges {
			if err := g.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
				return err
			}
		}
		total, path, err := g.Solve()
		if err != nil {
			return fmt.Errorf("selfcheck 用例 %d: %w", i, err)
		}
		if !pathLegal(g.g, path, total) { // 不变量 1：路径合法
			return fmt.Errorf("selfcheck 用例 %d: 路径不合法", i)
		}
		if best, _ := naiveMax(g.g); best != total { // 不变量 2、3：与朴素参照一致
			return fmt.Errorf("selfcheck 用例 %d: 总权 %d != 朴素参照 %d", i, total, best)
		}
	}
	return checkRejectKeepsState() // 不变量 4：失败不留痕
}

// pathLegal 核验节点序列相邻有边且边权之和等于 total。
func pathLegal(g *wdag.Graph, path []int, total int64) bool {
	if len(path) < 2 {
		return false
	}
	var sum int64
	for i := 0; i+1 < len(path); i++ {
		found := false
		for _, e := range g.OutEdges(path[i]) {
			if e.To == path[i+1] {
				sum += e.W
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return sum == total
}

// naiveMax 枚举所有至少含 1 条边的路径，返回最大总权（朴素参照）。
func naiveMax(g *wdag.Graph) (int64, bool) {
	var best int64
	have := false
	var dfs func(u int, sum int64, depth int)
	dfs = func(u int, sum int64, depth int) {
		if depth > 0 && (!have || sum > best) {
			best, have = sum, true
		}
		for _, e := range g.OutEdges(u) {
			dfs(e.To, sum+e.W, depth+1)
		}
	}
	for v := range g.N() {
		dfs(v, 0, 0)
	}
	return best, have
}

// checkRejectKeepsState 核验各类非法操作被拒绝且图状态不变。
func checkRejectKeepsState() error {
	if _, err := New(0); !errors.Is(err, wdag.ErrBadN) {
		return fmt.Errorf("selfcheck: n 非正未被拒绝")
	}
	g, _ := New(3)
	if err := g.AddEdge(0, 1, 7); err != nil {
		return err
	}
	before := g.EdgeCount()
	bads := []error{g.AddEdge(0, 3, 1), g.AddEdge(1, 1, 1), g.AddEdge(0, 1, 9)} // 越界/自环/重复
	for i, err := range bads {
		if err == nil {
			return fmt.Errorf("selfcheck: 非法边 %d 未被拒绝", i)
		}
	}
	if g.EdgeCount() != before {
		return fmt.Errorf("selfcheck: 拒绝后状态被改变")
	}
	if _, _, err := g.Solve(); err != nil {
		return fmt.Errorf("selfcheck: 拒绝后不可正常使用: %w", err)
	}
	return nil
}
