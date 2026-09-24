package main

import (
	"errors"
	"fmt"

	"ontology/hyper"
	"ontology/vec"
)

var fails int

func report(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	_, err := vec.Dist([]float64{1, 2}, []float64{1})
	report("维度不符可判定错误", errors.Is(err, vec.ErrDim))

	dim, L, b, n := 4, 3, 7, 11
	data := make([][]float64, n)
	for i := range data {
		data[i] = []float64{float64(i), float64(i) * .5, -float64(i), 1}
	}
	fa := hyper.NewFamily(dim, L, b, 7)
	fb := hyper.NewFamily(dim, L, b, 7)
	identical := true
	for _, v := range data {
		sa, e1 := fa.IndexSignatures(v)
		sb, e2 := fb.IndexSignatures(v)
		if e1 != nil || e2 != nil || len(sa) != len(sb) {
			identical = false
			break
		}
		for k := range sa {
			if sa[k] != sb[k] {
				identical = false
			}
		}
	}
	report("同种子重建签名逐字节相同", identical)
	report("哈希次数=N×L×b", fa.HashOps() == int64(n*L*b))
	if fails > 0 {
		fmt.Println("FAIL total")
		return
	}
	fmt.Println("OK total 3/9")
}
