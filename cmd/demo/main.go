package main

import (
	"fmt"
	"os"

	"ontology/cent"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	// 第 4 步后质心 [{1.5,2},{3,1},{4,1}]，n=4，r=1.5 落在两质心之间：线性插值
	cs := []cent.Centroid{{Mean: 1.5, Count: 2}, {Mean: 3, Count: 1}, {Mean: 4, Count: 1}}
	v, _, err := cent.Quantile(cs, 0.5)
	check("step4-interp", err == nil && v == 2.25, fmt.Sprintf("Q(0.5)=%v (不插值错值=1.5)", v))

	if failed {
		os.Exit(1)
	}
}
