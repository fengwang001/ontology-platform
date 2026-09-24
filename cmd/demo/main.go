package main

import "fmt"

type verdict struct {
	name string
	ok   bool
	ran  bool
}

func main() {
	results := []verdict{
		{name: "trailing-space-sample-1"},
		{name: "trailing-space-sample-2"},
		{name: "trailing-space-sample-3"},
		{name: "equals-escape-not-split"},
		{name: "76-char-limit-soft-equals"},
		{name: "five-error-types-and-offset"},
		{name: "all-split-points-consistent"},
		{name: "roundtrip"},
		{name: "minimal-escaping"},
		{name: "inspection-counter"},
	}

	pass := 0
	for _, r := range results {
		status := "SKIP"
		switch {
		case !r.ran:
		case r.ok:
			status = "OK"
			pass++
		default:
			status = "FAIL"
		}
		fmt.Printf("%s  %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
}
