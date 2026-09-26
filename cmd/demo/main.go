package main

import (
	"fmt"
	"math"
	"slices"

	"ontology/api"
	"ontology/sdot"
	"ontology/spv"
)

var failed bool

func check(ok bool, msg string) {
	if ok {
		fmt.Println("OK:", msg)
	} else {
		failed = true
		fmt.Println("FAIL:", msg)
	}
}

func denseDot(m int, a, b *spv.Vec) float64 {
	sum := 0.0
	for k := 0; k < m; k++ {
		sum += a.Get(k) * b.Get(k)
	}
	return sum
}

// buggyJia 复现 (甲)：循环上界 i<lenA-1，漏掉最后一个公共下标。
func buggyJia(ai []int, av []float64, bi []int, bv []float64) float64 {
	i, j, sum := 0, 0, 0.0
	for i < len(ai)-1 && j < len(bi)-1 {
		switch {
		case ai[i] == bi[j]:
			sum += av[i] * bv[j]
			i, j = i+1, j+1
		case ai[i] < bi[j]:
			i++
		default:
			j++
		}
	}
	return sum
}

func main() {
	const m = 10
	a, b := spv.New(m), spv.New(m)
	for k, ix := range []int{1, 3, 5} {
		_ = a.Set(ix, []float64{2, 3, 5}[k])
	}
	for k, ix := range []int{0, 1, 5} {
		_ = b.Set(ix, []float64{4, 7, 6}[k])
	}
	ai, av := a.Snapshot()
	bi, bv := b.Snapshot()
	wantCum, gotCum := []float64{0, 14, 14, 44}, []float64{}
	i, j, sum := 0, 0, 0.0
	for i < len(ai) && j < len(bi) {
		switch {
		case ai[i] == bi[j]:
			sum += av[i] * bv[j]
			i, j = i+1, j+1
		case ai[i] < bi[j]:
			i++
		default:
			j++
		}
		gotCum = append(gotCum, sum)
	}
	traceOK := len(gotCum) == len(wantCum)
	for k := range wantCum {
		traceOK = traceOK && gotCum[k] == wantCum[k]
	}
	check(traceOK, "merge trace cum=[0,14,14,44], dot=44")
	// (甲) 错误上界；(乙) 忽略 idx 按位置对齐；(丙) 不等时双进。
	jia, yi, bing := buggyJia(ai, av, bi, bv), 0.0, 0.0
	for k := 0; k < len(av); k++ {
		yi += av[k] * bv[k]
	}
	for pi, pj := 0, 0; pi < len(ai) && pj < len(bi); pi, pj = pi+1, pj+1 {
		if ai[pi] == bi[pj] {
			bing += av[pi] * bv[pj]
		}
	}
	check(jia == 14 && yi == 59 && bing == 30,
		fmt.Sprintf("variants A=%v B=%v C=%v", jia, yi, bing))
	check(sdot.Dot(a, b) == denseDot(m, a, b) && a.Canonical() && b.Canonical(),
		"dot equals dense reference; canonical form")
	nnzOK, ref := true, 0.0
	for _, mm := range []int{100, 1000, 10000} {
		x, y := spv.New(mm), spv.New(mm)
		for k := 0; k < 10; k++ {
			ix := (k*7 + 3) % mm
			_ = x.Set(ix, float64(k+1))
			_ = y.Set(ix, float64(10-k))
		}
		xi, _ := x.Snapshot()
		yi2, _ := y.Snapshot()
		d := sdot.Dot(x, y)
		nnzOK = nnzOK && len(xi) == 10 && len(yi2) == 10 && d == denseDot(mm, x, y)
		ref = d
	}
	check(nnzOK && ref > 0, "stored=20 entries, dot correct at m=100..10000")
	bits := math.Float64bits(sdot.Dot(a, b))
	const n = 64
	res := make(chan uint64, n)
	for g := 0; g < n; g++ {
		go func() { res <- math.Float64bits(sdot.Dot(a, b)) }()
	}
	concOK := true
	for g := 0; g < n; g++ {
		if r := <-res; r != bits {
			concOK = false
		}
	}
	check(concOK, "64 concurrent dots byte-identical")
	cases := []struct {
		m    int
		idx  []int
		val  []float64
		want error
	}{
		{10, []int{10}, []float64{1}, spv.ErrIndexOutOfRange},
		{10, []int{-1}, []float64{1}, spv.ErrIndexOutOfRange},
		{10, []int{1}, []float64{0}, api.ErrZeroValue},
		{10, []int{1, 2}, []float64{1}, api.ErrLenMismatch},
		{10, []int{2, 1}, []float64{1, 2}, api.ErrIndexNotSorted},
		{10, []int{1, 1}, []float64{1, 2}, api.ErrIndexNotSorted},
	}
	errOK := true
	for _, c := range cases {
		v, err := api.Build(c.m, c.idx, c.val)
		errOK = errOK && v == nil && err == c.want
	}
	i0, v0 := a.Snapshot()
	errOK = errOK && a.Set(-1, 9) == spv.ErrIndexOutOfRange && a.Set(10, 9) == spv.ErrIndexOutOfRange
	i1, v1 := a.Snapshot()
	errOK = errOK && slices.Equal(i0, i1) && slices.Equal(v0, v1)
	_ = a.Set(3, 9)
	errOK = errOK && a.Get(3) == 9 && a.Canonical()
	check(errOK, "4 sentinel errors; rejected ops leave no trace; still usable")
	check(api.SelfCheck() == nil, "api.SelfCheck passes all 4 invariants")
	if failed {
		fmt.Println("DEMO FAILED")
	}
}
