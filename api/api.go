package api // 对外门面：有向图加边、Floyd–Warshall 闭包计算与 O(1) 查询。依赖 tc、dg。

import (
	"errors"
	"sync"

	"ontology/dg"
	"ontology/tc"
)

var ErrInvalidN = errors.New("api: n must be positive") // n 非正；与 dg 的三个边错误构成四类互不相同的哨兵

type API struct {
	n  int
	g  *dg.Graph
	mu sync.RWMutex
	t  *tc.TC // 最近一次 Compute 的闭包；成功 AddEdge 后失效
}

// New 创建 n 个节点的实例；n 非正时返回 ErrInvalidN。
func New(n int) (*API, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	return &API{n: n, g: dg.New(n)}, nil
}

// AddEdge 登记 u→v；越界/自环/重复边返回互不相同哨兵，校验先于写入，拒绝不留痕。
func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.g.AddEdge(u, v); err != nil {
		return err
	}
	a.t = nil
	return nil
}

// Compute 一次性算出传递闭包；之后 Reach 为 O(1)。
func (a *API) Compute() {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := tc.New(a.g) // 锁内快照，闭包对应确定的图状态
	t.Compute()
	a.t = t
}

// Reach 回答是否存在 i→j 的至少含 1 条边的路径；尚未 Compute 时惰性计算。
func (a *API) Reach(i, j int) bool {
	a.mu.RLock()
	t := a.t
	a.mu.RUnlock()
	if t == nil {
		a.Compute()
		a.mu.RLock()
		t = a.t
		a.mu.RUnlock()
	}
	return t.Reach(i, j)
}

func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

func naiveRef(n int, edges [][2]int) [][]bool {
	adj := make([][]bool, n)
	for i := range adj {
		adj[i] = make([]bool, n)
	}
	for _, e := range edges {
		adj[e[0]][e[1]] = true
	}
	ref := make([][]bool, n)
	for s := 0; s < n; s++ {
		ref[s] = make([]bool, n)
		seen := make([]bool, n)
		q := []int{}
		for v := 0; v < n; v++ { // 只从直接边入队：空路径不算可达
			if adj[s][v] {
				seen[v], q = true, append(q, v)
			}
		}
		for len(q) > 0 {
			u := q[0]
			q = q[1:]
			ref[s][u] = true
			for v := 0; v < n; v++ {
				if adj[u][v] && !seen[v] {
					seen[v], q = true, append(q, v)
				}
			}
		}
	}
	return ref
}

// SelfCheck 对内置图核验四条不变量：定义正确、与朴素 BFS 逐格一致、传递性、四类拒绝互不相同且失败不留痕。
func (a *API) SelfCheck() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidN) {
		return errors.New("api: selfcheck invalid-n mismatch")
	}
	// 两组边均取 n=5：第一组含环与长链，第二组含二连环与孤立节点 4。
	cases := [][][2]int{
		{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}},
		{{0, 1}, {1, 0}, {2, 3}},
	}
	for _, edges := range cases {
		x, err := New(5)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if err := x.AddEdge(e[0], e[1]); err != nil {
				return err
			}
		}
		x.Compute()
		ref := naiveRef(5, edges)
		for i := 0; i < 5; i++ { // 不变量 1、2、3
			for j := 0; j < 5; j++ {
				got := x.Reach(i, j)
				if got != ref[i][j] {
					return errors.New("api: selfcheck closure mismatch")
				}
				for k := 0; k < 5; k++ {
					if x.Reach(i, k) && x.Reach(k, j) && !got {
						return errors.New("api: selfcheck transitivity violated")
					}
				}
			}
		}
		before := x.EdgeCount() // 不变量 4：哨兵互不相同、拒绝不留痕
		if !errors.Is(x.AddEdge(-1, 0), dg.ErrNodeOutOfRange) ||
			!errors.Is(x.AddEdge(0, 0), dg.ErrSelfLoop) {
			return errors.New("api: selfcheck rejection mismatch")
		}
		if len(edges) > 0 {
			if e := edges[0]; !errors.Is(x.AddEdge(e[0], e[1]), dg.ErrDuplicateEdge) {
				return errors.New("api: selfcheck duplicate mismatch")
			}
		}
		if x.EdgeCount() != before {
			return errors.New("api: selfcheck state changed after rejection")
		}
	}
	return nil
}
