// Command demo 逐条打印 KM 实现的自检结论，每条一行 OK/FAIL。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/bmg"
	"ontology/km"
)

var spec = [][]int64{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}}

func greedy(w [][]int64) (int64, []int) { // 按左序取未占用右中权最大（并列取小），不回溯
	n := len(w)
	m := make([]int, n)
	used := make([]bool, n)
	var sum int64
	for l := 0; l < n; l++ {
		best := -1
		for r := 0; r < n; r++ {
			if !used[r] && (best < 0 || w[l][r] > w[l][best]) {
				best = r
			}
		}
		m[l], used[best], sum = best, true, sum+w[l][best]
	}
	return sum, m
}
func bruteMin(w [][]int64) (int64, []int) { // 枚举 6 种排列取最小权（n=3 演示用）
	var bs int64
	var bm []int
	for _, q := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		s := w[0][q[0]] + w[1][q[1]] + w[2][q[2]]
		if bm == nil || s < bs {
			bs, bm = s, []int{q[0], q[1], q[2]}
		}
	}
	return bs, bm
}
func line(tag string, ok bool) bool {
	if ok {
		fmt.Println("OK  " + tag)
	} else {
		fmt.Println("FAIL " + tag)
	}
	return ok
}
func step2Delta(w [][]int64) (int64, int64) { // 复现增广左1：正确只取 S 内左，错误混入树外左2
	l := make([]int64, len(w))
	for i := range w {
		l[i] = w[i][0]
		for _, x := range w[i][1:] {
			if x > l[i] {
				l[i] = x
			}
		}
	}
	minOver := func(lefts []int) int64 { // 树外右 j∈{1,2}
		d := int64(1 << 62)
		for _, i := range lefts {
			for _, j := range []int{1, 2} {
				if v := l[i] - w[i][j]; v < d {
					d = v
				}
			}
		}
		return d
	}
	return minOver([]int{0, 1}), minOver([]int{0, 1, 2}) // (正确 S, 错误含左2)
}
func fill(m *api.Matcher, w [][]int64) {
	for l := range w {
		for r, v := range w[l] {
			_ = m.SetWeight(l, r, v)
		}
	}
}
func main() {
	all := true
	m, _ := api.New(3)
	fill(m, spec)
	sum, mt, _ := m.Solve()
	gs, gm := greedy(spec)
	ls, lm := bruteMin(spec)
	dc, dw := step2Delta(spec)
	all = line("题三最大权 28 匹配 [1 0 2]", sum == 28 && slices.Equal(mt, []int{1, 0, 2})) && all
	all = line("贪心陷阱错值 20 / [0 1 2]", gs == 20 && slices.Equal(gm, []int{0, 1, 2})) && all
	all = line("最小权陷阱错值 0 / [2 1 0]", ls == 0 && slices.Equal(lm, []int{2, 1, 0})) && all
	all = line(fmt.Sprintf("delta 混入树外左节点错为 %d（正确 %d）", dw, dc), dw == 0 && dc == 2) && all

	_, eN := api.New(0)
	eRange, eDup := m.SetWeight(9, 9, 1), m.SetWeight(0, 0, 1)
	mm, _ := api.New(1)
	_, _, eInc := mm.Solve()
	four := errors.Is(eN, api.ErrInvalidN) && errors.Is(eRange, bmg.ErrNodeRange) &&
		errors.Is(eDup, bmg.ErrDuplicate) && errors.Is(eInc, api.ErrIncomplete) &&
		eN.Error() != eRange.Error() && eRange.Error() != eDup.Error() && eDup.Error() != eInc.Error()
	all = line("四类错误可判定且互不相同", four) && all

	d, _ := api.New(2) // 越界/重复被拒不落盘，补全后 [[1,3],[4,5]] 最优 [1,0]=7
	_ = d.SetWeight(0, 0, 1)
	_ = d.SetWeight(9, 0, 1)
	_ = d.SetWeight(0, 0, 2)
	fill(d, [][]int64{{0, 3}, {4, 5}}) // (0,0) 已为 1
	s2, m2, _ := d.Solve()
	all = line("被拒后状态不变仍可用 (7, [1 0])", s2 == 7 && slices.Equal(m2, []int{1, 0})) && all
	all = line("大 n 求 delta 检查右节点数 ≤2 不随 n 增长", km.VerifyDeltaBound([]int{100, 1000, 10000}, 2)) && all

	n, N := 60, 16
	big, _ := api.New(n)
	w := make([][]int64, n)
	for l := range w {
		w[l] = make([]int64, n)
		for r := range w[l] {
			w[l][r] = int64((l*7+r*13)%97) - 48
		}
	}
	fill(big, w)
	var wg sync.WaitGroup
	sums := make([]int64, N)
	ms := make([][]int, N)
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; sums[g], ms[g], _ = big.Solve() }(g)
	}
	close(start)
	wg.Wait()
	consistent := true
	for g := 1; g < N; g++ {
		consistent = consistent && sums[g] == sums[0] && slices.Equal(ms[g], ms[0])
	}
	all = line("16 goroutine 并发 Solve 结果逐元素一致", consistent) && all
	if !all {
		os.Exit(1)
	}
}
