package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
}

var sec3edges = [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}}

// sec3api 建第三节七边图；extra 为 true 时再加新源边 5→6:100（节点 5,6 否则孤立，不影响答案）。
func sec3api(extra bool) *api.API {
	a, _ := api.New(7)
	for _, e := range sec3edges {
		_ = a.AddEdge(int(e[0]), int(e[1]), e[2])
	}
	if extra {
		_ = a.AddEdge(5, 6, 100)
	}
	return a
}

func main() {
	a, _ := api.New(3)
	_ = a.AddEdge(0, 1, 5)
	cyc, _ := api.New(2)
	_ = cyc.AddEdge(0, 1, 1)
	_ = cyc.AddEdge(1, 0, 1)
	_, _, errCyc := cyc.Solve()
	empty, _ := api.New(2)
	_, _, errEmpty := empty.Solve()
	_, e0 := api.New(0)
	bads := []error{e0, a.AddEdge(0, 3, 1), a.AddEdge(1, 1, 1), a.AddEdge(0, 1, 9), errCyc, errEmpty}
	wants := []error{api.ErrBadOrder, api.ErrNodeOutOfRange, api.ErrSelfLoop, api.ErrDuplicateEdge, api.ErrCycle, api.ErrNoPath}
	ok := true
	for i := range bads {
		for j := range wants {
			ok = ok && (errors.Is(bads[i], wants[j]) == (i == j))
		}
	}
	check("四类错误+非法n+含环+无边 可判定且互不相同", ok)

	_, _, err := a.Solve() // 被拒后状态不变且可继续用
	check("被拒后状态不变且可继续用", a.EdgeCount() == 1 && err == nil)

	total, path, err := sec3api(false).Solve()
	check(fmt.Sprintf("第三节 总权=%d 路径=%v", total, path),
		err == nil && total == 11 && fmt.Sprint(path) == "[0 1 4]")
	t2, _, _ := sec3api(true).Solve()
	st, fx, lx := shortestTrap(), fixedSrcTrap(), lexMaxTrap()
	check(fmt.Sprintf("陷阱(甲)最短路错值=%d 正确=11", st), st == 8)
	check(fmt.Sprintf("陷阱(乙)固定源0错值=%d 正确=%d", fx, t2), fx == 11 && t2 == 100)
	check(fmt.Sprintf("陷阱(丙)字典序反向错值=%v 正确=[0 1 4]", lx), fmt.Sprint(lx) == "[0 2 4]")

	big, _ := api.New(10000)
	for i := 0; i+1 < 10000; i++ {
		_ = big.AddEdge(i, i+1, 1)
	}
	bt, bp, err := big.Solve()
	check("大m=10000 前驱枚举不随m增长", err == nil && bt == 9999 && len(bp) == 10000)

	shared := sec3api(false)
	res := make(chan string, 8)
	for i := 0; i < 8; i++ {
		go func() {
			t, p, err := shared.Solve()
			res <- fmt.Sprint(t, p, err)
		}()
	}
	first, same := "", true
	for i := 0; i < 8; i++ {
		if r := <-res; i == 0 {
			first = r
		} else if r != first {
			same = false
		}
	}
	check("并发 Solve 结果逐元素一致", same)

	if failed {
		os.Exit(1)
	}
}

// shortestTrap 复现(甲)：同样的拓扑序 DP 但取 min，返回 dist[4]。
func shortestTrap() int64 {
	d := [5]int64{0, int64(1) << 62, int64(1) << 62, int64(1) << 62, int64(1) << 62}
	for _, e := range sec3edges {
		if c := d[e[0]] + e[2]; c < d[e[1]] {
			d[e[1]] = c
		}
	}
	return d[4]
}

// fixedSrcTrap 复现(乙)：只允许从 0 出发（其它源 dist=-inf），返回最大 dist。
func fixedSrcTrap() int64 {
	d := make([]int64, 7)
	for i := range d {
		d[i] = int64(-1) << 62
	}
	d[0] = 0
	best := int64(-1) << 62
	for u := 0; u < 7; u++ {
		for _, e := range slices.Concat(sec3edges, [][3]int64{{5, 6, 100}}) {
			if c := d[u] + e[2]; int(e[0]) == u && c > d[e[1]] {
				d[e[1]] = c
			}
		}
		if u > 0 && d[u] > best {
			best = d[u]
		}
	}
	return best
}

// lexMaxTrap 复现(丙)：枚举全部路径，并列最大总权时取字典序最大的序列。
func lexMaxTrap() []int {
	adj := [5][][2]int64{}
	for _, e := range sec3edges {
		adj[e[0]] = append(adj[e[0]], [2]int64{e[1], e[2]})
	}
	var best []int
	bestSum := int64(-1) << 62
	var dfs func(u int, sum int64, cur []int)
	dfs = func(u int, sum int64, cur []int) {
		if len(cur) > 1 && (sum > bestSum || (sum == bestSum && slices.Compare(cur, best) > 0)) {
			bestSum, best = sum, append([]int(nil), cur...)
		}
		for _, e := range adj[u] {
			dfs(int(e[0]), sum+e[1], append(cur, int(e[0])))
		}
	}
	for v := 0; v < 5; v++ {
		dfs(v, 0, []int{v})
	}
	return best
}
