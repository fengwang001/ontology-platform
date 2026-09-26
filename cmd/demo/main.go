package main

import (
	"fmt"
	"math"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/pivot"
)

var fails int

func check(name string, ok bool, detail string) {
	if !ok {
		fails++
	}
	fmt.Printf("%s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name, detail)
}

// naive 按第一行展开的递归定义（朴素参照）。
func naive(a []float64, n int) float64 {
	if n == 1 {
		return a[0]
	}
	var sum float64
	for j := 0; j < n; j++ {
		var sub []float64
		for i := 1; i < n; i++ {
			for c := 0; c < n; c++ {
				if c != j {
					sub = append(sub, a[i*n+c])
				}
			}
		}
		sum += (1 - 2*float64(j%2)) * a[j] * naive(sub, n-1)
	}
	return sum
}

func swapRows(m []float64, n, r, k int) {
	for j := 0; j < n; j++ {
		m[k*n+j], m[r*n+j] = m[r*n+j], m[k*n+j]
	}
}

// buggy 复现第三节三个错误实现：0=忘符号翻转 1=不选主元 2=漏乘末主元。
func buggy(a []float64, n, mode int) float64 {
	m := append([]float64(nil), a...)
	sign := 1.0
	for k := 0; k < n; k++ {
		r := k
		if mode != 1 {
			r, _ = pivot.Pick(m, n, k)
		}
		if r != k {
			swapRows(m, n, r, k)
			if mode != 0 {
				sign = -sign
			}
		}
		p := m[k*n+k]
		for i := k + 1; i < n; i++ {
			if f := m[i*n+k] / p; f != 0 {
				for j := k; j < n; j++ {
					m[i*n+j] -= f * m[k*n+j]
				}
			}
		}
	}
	d := sign
	for k := 0; k < n-mode/2; k++ { // mode=2 时漏乘最后一个主元
		d *= m[k*n+k]
	}
	return d
}

func main() {
	x := api.New()
	A := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}

	// 第三节逐步消元：k=0 换(0,1) 符号-；k=1 并列取行1；k=2 对角 -2。
	m := append([]float64(nil), A...)
	sign, trace := 1.0, ""
	for k := 0; k < 3; k++ {
		r, _ := pivot.Pick(m, 3, k)
		sw := ""
		if r != k {
			swapRows(m, 3, r, k)
			sign = -sign
			sw = fmt.Sprintf(" swap(%d,%d)", k, r)
		}
		p := m[k*3+k]
		for i := k + 1; i < 3; i++ {
			f := m[i*3+k] / p
			for j := k; j < 3; j++ {
				m[i*3+j] -= f * m[k*3+j]
			}
		}
		trace += fmt.Sprintf("k%d:piv=r%d%s sign=%v row=%v;", k, r, sw, sign, m[k*3:k*3+3])
	}
	prod := m[0] * m[4] * m[8]
	check("steps", prod == -2 && sign*prod == 2, fmt.Sprintf("%s prod=%v det=%v", trace, prod, sign*prod))
	b0, b1, b2 := buggy(A, 3, 0), buggy(A, 3, 1), buggy(A, 3, 2)
	check("甲乙丙", b0 == -2 && math.IsNaN(b1) && b2 == -1,
		fmt.Sprintf("甲忘符号=%v 乙无主元=%v 丙漏末主元=%v (正确=2)", b0, b1, b2))

	d, err := x.Det(A, 3)
	check("naive一致", err == nil && d == naive(A, 3) && d == 2, fmt.Sprintf("det=%v", d))
	before := append([]float64(nil), A...)
	_, _ = x.Det(A, 3)
	check("输入未修改", slices.Equal(A, before), "")

	_, e1 := x.Det(A, 0)
	_, e2 := x.Det([]float64{1, 2, 3}, 2)
	_, e3 := x.Det([]float64{math.Inf(1), 0, 0, 1}, 2)
	distinct := e1 != e2 && e2 != e3 && e1 != e3
	check("三类错误可判定", e1 == api.ErrEmpty && e2 == api.ErrDimension && e3 == api.ErrNonFinite && distinct,
		fmt.Sprintf("%v|%v|%v", e1, e2, e3))
	d2, err2 := x.Det(A, 3)
	check("拒后状态不变", err2 == nil && d2 == 2, fmt.Sprintf("det=%v", d2))

	tri := make([]float64, 1000*1000)
	for i := range 1000 {
		tri[i*1000+i] = 2
	}
	dt, _ := x.Det(tri, 1000)
	check("大n上三角", dt == math.Pow(2, 1000), "det=2^1000 乘减恒0由白盒测试钉住")
	const G = 16
	var wg sync.WaitGroup
	bits := make([]uint64, G)
	for g := range G {
		wg.Go(func() {
			v, _ := x.Det(A, 3)
			bits[g] = math.Float64bits(v)
		})
	}
	wg.Wait()
	agree := true
	for _, b := range bits {
		agree = agree && b == bits[0]
	}
	check("并发一致", agree, fmt.Sprintf("%d goroutines bits=%x", G, bits[0]))
	check("SelfCheck", x.SelfCheck() == nil, "")
	if fails > 0 {
		os.Exit(1)
	}
}
