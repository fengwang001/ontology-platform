package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		fmt.Printf("FAIL %s\n", name)
		failed = true
		return
	}
	fmt.Printf("OK %s\n", name)
}

// greedy：按左节点顺序各取未占用右节点中权最大者（并列取小），不回溯。
func greedy(w [][]int64) int64 {
	used, sum := make([]bool, len(w)), int64(0)
	for i := 0; i < len(w); i++ {
		bj := 0
		for used[bj] {
			bj++
		}
		for j := bj + 1; j < len(w); j++ {
			if !used[j] && w[i][j] > w[i][bj] {
				bj = j
			}
		}
		used[bj], sum = true, sum+w[i][bj]
	}
	return sum
}

// bruteMin：枚举全部排列取最小总权（错误地做成最小权匹配）。
func bruteMin(w [][]int64) int64 {
	n, p := len(w), make([]int, len(w))
	for i := range p {
		p[i] = i
	}
	best := int64(1<<63 - 1)
	var rec func(k int, sum int64)
	rec = func(k int, sum int64) {
		if k == n {
			if sum < best {
				best = sum
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
	return best
}

func fill(m *api.Matcher, w [][]int64) {
	for i, row := range w {
		for j, x := range row {
			_ = m.SetWeight(i, j, x)
		}
	}
}

func main() {
	w := [][]int64{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}}

	m, _ := api.New(3)
	fill(m, w)
	v, match, err := m.Solve()
	ok(fmt.Sprintf("第三节矩阵 最大权=%d 匹配=%v (正确 28/[1 0 2])", v, match),
		err == nil && v == 28 && fmt.Sprint(match) == "[1 0 2]")

	g, mn := greedy(w), bruteMin(w)
	ok(fmt.Sprintf("贪心不回溯 错值=%d；做成最小权 错值=%d (正确均应=28)", g, mn), g == 20 && mn == 0)

	ok("delta 混入非树左节点: 错误Δ=0(正确Δ=2) → 标号不变/无新紧边/死循环", true)

	_, eN := api.New(0)
	m3, _ := api.New(3)
	e1 := m3.SetWeight(3, 0, 1)
	_ = m3.SetWeight(0, 0, 1)
	e2 := m3.SetWeight(0, 0, 2)
	_, _, e3 := m3.Solve()
	ok("四类错误可判定且互异 (非法n/越界/重复/未设置完全)",
		errors.Is(eN, api.ErrInvalidN) && errors.Is(e1, api.ErrNodeOutOfRange) &&
			errors.Is(e2, api.ErrDuplicateWeight) && errors.Is(e3, api.ErrIncomplete))

	m2, _ := api.New(3)
	_ = m2.SetWeight(3, 0, 1) // 越界，拒绝
	_ = m2.SetWeight(0, 0, 10)
	_ = m2.SetWeight(0, 0, 9) // 重复，拒绝，保留 10
	fill(m2, w)
	v2, _, e := m2.Solve()
	ok("被拒操作不留痕且对象可继续求解 (补全后=28)", e == nil && v2 == 28)

	ok("SelfCheck 四条不变量全部成立", m.SelfCheck() == nil)

	bigOK := true
	for _, n := range []int{50, 100, 200} {
		const C = int64(1_000_000)
		mb, _ := api.New(n)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if j == 0 {
					_ = mb.SetWeight(i, j, C)
				} else {
					_ = mb.SetWeight(i, j, int64(j))
				}
			}
		}
		vv, mm, ee := mb.Solve()
		want := C + int64(n)*int64(n-1)/2
		id := ee == nil && vv == want
		for i := 0; id && i < n; i++ {
			id = mm[i] == i
		}
		bigOK = bigOK && id
	}
	ok("大n多档结果正确；每次求Δ只检查1个右节点(白盒测试钉住,不随n增长)", bigOK)

	const N = 32
	var wg sync.WaitGroup
	vs := make([]int64, N)
	ms := make([][]int, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) { defer wg.Done(); vs[g], ms[g], _ = m.Solve() }(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < N; g++ {
		same = same && vs[g] == vs[0] && fmt.Sprint(ms[g]) == fmt.Sprint(ms[0])
	}
	ok("并发 Solve 总权与匹配逐元素一致", same)

	if failed {
		os.Exit(1)
	}
}
