package main

import (
	"fmt"

	"ontology/hagg"
	"ontology/hst"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// hst: boundary semantics — exact lower bound, exact maxValue, negative.
	b0, ok0 := hst.Bucket(10, 10, 100)    // exact k*W -> bucket k
	bOf, okOf := hst.Bucket(100, 10, 100) // v == maxValue -> overflow
	_, okNeg := hst.Bucket(-1, 10, 100)   // negative -> illegal
	check("hst-boundary", ok0 && b0 == 1 && okOf && bOf == 10 && !okNeg)

	// hagg: bucket disappearance + constant-time location under large m.
	h, _ := hagg.New(10, 100, 64)
	_ = h.Add(25)
	_ = h.Remove(25)
	_, gone := h.Count(2)
	check("hagg-bucket-disappears", !gone && len(h.Buckets()) == 0)
	check("hagg-constant-time-locate", hagg.CheckConstantTimeLocate())
}
