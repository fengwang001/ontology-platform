// Command demo 对分块矩阵乘法做非联网、无参数的端到端判定演示。
// 退出码 0 表示全部判定通过；输出不超过 10 行，每行一条 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/blk"
	"ontology/mul"
)

var failed bool

func report(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}
func bitsEqual(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return true
}

// formulaMatrix 用确定性公式生成行主序 n*n 矩阵（含负、零、正）。
func formulaMatrix(n int, f func(i, j int) float64) []float64 {
	m := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			m[i*n+j] = f(i, j)
		}
	}
	return m
}

func main() {
	const n = 5
	a := formulaMatrix(n, func(i, j int) float64 { return float64(i*n + j + 1) })
	b := formulaMatrix(n, func(i, j int) float64 { return float64(i - j) })

	// 1) 第三节五行累加：逐 k 部分和与最终 C[3][2]。
	var partial []float64
	var s float64
	for k := 0; k < n; k++ {
		s += a[3*n+k] * b[k*n+2]
		partial = append(partial, s)
	}
	c := mul.Blocked(a, b, n, 2)
	report(bitsEqual(partial, []float64{-32, -49, -49, -30, 10}) && c[3*n+2] == 10,
		"五行部分和=%v 最终 C[3][2]=%v", partial, c[3*n+2])

	// 2) (甲) 丢尾块 / (乙) 重复 k=2,4 / (丙) 读 B 转置。
	var bing float64
	for k := 0; k < n; k++ {
		bing += float64(16+k) * float64(2-k)
	}
	report(partial[3] == -30 && 10+0+40 == 50 && bing == -10,
		"错值 (甲)=%v (乙)=50 (丙)=%v", partial[3], bing)

	// 3) 多档 n/b（含尾块）Blocked 与 Naive 逐位一致。
	matchOK := true
	for _, d := range []struct{ n, bs int }{{5, 2}, {6, 4}, {17, 7}} {
		x := formulaMatrix(d.n, func(i, j int) float64 { return float64(i*d.n + j - d.n) })
		y := formulaMatrix(d.n, func(i, j int) float64 { return float64(j - i) })
		if !bitsEqual(mul.Blocked(x, y, d.n, d.bs), mul.Naive(x, y, d.n)) {
			matchOK = false
		}
	}
	report(matchOK, "Blocked 与 Naive 多档逐位一致")

	// 4) Parts 精确划分 [0,n)，尾块大小 = n%b。
	coverOK := true
	for _, d := range []struct{ n, b int }{{5, 2}, {17, 17}, {100, 17}} {
		ps := blk.Parts(d.n, d.b)
		seen := make([]int, d.n)
		for i, p := range ps {
			if (i > 0 && ps[i-1].End != p.Start) || p.End-p.Start > d.b {
				coverOK = false
			}
			for x := p.Start; x < p.End; x++ {
				seen[x]++
			}
		}
		for _, v := range seen {
			if v != 1 {
				coverOK = false
			}
		}
		if r := d.n % d.b; r != 0 && ps[len(ps)-1].End-ps[len(ps)-1].Start != r {
			coverOK = false
		}
	}
	report(coverOK, "块覆盖完备（无重叠/缝隙，尾块大小=n%%b）")

	// 5) 三类可判定错误互不相同。
	e := api.New(2)
	_, eEmpty := e.Mul(a, b, 0)
	_, eBlock := api.New(6).Mul(a, b, n)
	_, eShape := e.Mul(a[:n*n-1], b, n)
	report(errors.Is(eEmpty, api.ErrEmptyMatrix) && errors.Is(eBlock, api.ErrBlockSize) &&
		errors.Is(eShape, api.ErrShapeMismatch) && !errors.Is(eEmpty, eBlock),
		"三类可判定错误（空矩阵/块越界/维度不符）互不相同")

	// 6) 被拒后状态不变。
	baseline, _ := e.Mul(a, b, n)
	_, _ = e.Mul(a, b, 0)
	_, _ = e.Mul(a[:n*n-1], b, n)
	after, _ := e.Mul(a, b, n)
	report(bitsEqual(after, baseline), "被拒后状态不变，引擎可继续正常使用")

	// 7) 大 n 下尾块定位闭式 O(1)（只得到通过与否，读不到计数器数值）。
	report(blk.SelfCheck() == nil, "大 n(100/1000/10000) 定位尾块访问边界条目为 0")

	// 8) 并发只读一致：16 goroutine 同一份输入，结果逐字节相同。
	const g = 16
	res := make([][]float64, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; res[i], _ = e.Mul(a, b, n) }(i)
	}
	close(start)
	wg.Wait()
	concOK := true
	for _, r := range res {
		concOK = concOK && bitsEqual(r, baseline)
	}
	report(concOK, "并发只读一致（16 goroutine 结果逐字节相同）")

	// 9) api.SelfCheck 自检四不变量。
	report(api.New(2).SelfCheck() == nil, "api.SelfCheck 四不变量自检通过")

	if failed {
		os.Exit(1)
	}
}
