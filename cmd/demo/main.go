package main

import (
	"fmt"
	"os"

	"ontology/bit"
	"ontology/check"
	"ontology/idx"
)

var failed bool

func judge(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	b, err := bit.New(5)
	judge("New(5)", err == nil)
	judge("Add", b.Add(2, 7) == nil && b.Add(4, 3) == nil)
	sum, err := b.PrefixSum(4)
	judge("PrefixSum", err == nil && sum == 10)
	r, err := b.RangeSum(2, 4)
	judge("RangeSum", err == nil && r == 10)
	_, err = bit.New(0)
	judge("New(0)-legal", err == nil)
	judge("idx.Check", idx.Index(4).Check(b) == nil && idx.Index(5).Check(b) != nil)
	nv := check.NewNaive(5)
	nv.Add(2, 7)
	nv.Add(4, 3)
	judge("naive-match", nv.PrefixSum(4) == sum && nv.RangeSum(2, 4) == r)
	if failed {
		fmt.Println("FAIL total")
		os.Exit(1)
	}
	fmt.Println("OK total")
}
