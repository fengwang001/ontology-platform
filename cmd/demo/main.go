// Command demo 逐项演示并判定最短往返编解码器的正确性。
package main

import (
	"fmt"
	"math"
	"os"

	"ontology/bits"
	"ontology/dec"
)

var failures int

func check(ok bool, label string) {
	if ok {
		fmt.Printf("OK %s\n", label)
	} else {
		fmt.Printf("FAIL %s\n", label)
		failures++
	}
}

func main() {
	p := bits.Split(1)
	s := bits.Split(math.SmallestNonzeroFloat64)
	check(p.Kind == bits.Normal && p.Mant == 1<<52 && p.Exp == -52 &&
		s.Kind == bits.Subnormal && s.Mant == 1 && s.Exp == -1074,
		"bits: 1 与最小次正规数精确分解")

	dec.ResetChecks()
	dec.Shortest(0.1)
	check(dec.Checks() <= 3, "dec: 0.1 精确往返检查不超三次")

	trap := math.Float64frombits(0xa8b621587cb3ad0b)
	naive := -1437831775509385.0 / math.Pow(10, 127)
	d, _ := dec.Shortest(trap)
	check(naive == trap && len(d) == 17, "dec: 浮点回代误判例子给出正确 17 位")

	fmt.Printf("TOTAL %d failures\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
