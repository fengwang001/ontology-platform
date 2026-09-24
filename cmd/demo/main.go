package main

import (
	"fmt"
	"os"

	"ontology/props"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	results = append(results, check{"skeleton", props.New() != nil})

	fail := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(results)-fail, len(results))
	if fail > 0 {
		os.Exit(1)
	}
}
