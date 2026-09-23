package main

import (
	"fmt"
	"os"

	"ontology/attrib"
	"ontology/tree"
)

func main() {
	failures := 0
	check := func(label string, ok bool) {
		if ok {
			fmt.Println("OK  " + label)
		} else {
			failures++
			fmt.Println("FAIL " + label)
		}
	}

	tr := tree.New(0)
	tr.InsertNames("A", "F", "G", "F", "H")
	check("self sum == samples (1==1)", tr.Root.SelfSum() == 1 && tr.Samples == 1)
	check("total sum (5) > self sum (1)", tr.Root.TotalSum() == 5 && tr.Root.TotalSum() > tr.Root.SelfSum())

	funcTotal := int64(-1)
	for _, f := range attrib.Functions(tr) {
		if f.Name == "F" {
			funcTotal = f.Total
		}
	}
	check("recursive F function total == outer only (1)", funcTotal == 1)

	if failures > 0 {
		fmt.Printf("TOTAL %d FAIL\n", failures)
		os.Exit(1)
	}
	fmt.Println("TOTAL all checks passed")
}
