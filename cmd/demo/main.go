package main

import "fmt"

func main() {
	checks := []string{
		"skeleton",
		"roundtrip",
		"tri-state",
		"prune-1000->1",
		"exhaustive",
		"null-predicate",
		"cursor-batches",
		"truncation",
		"bitwidth",
		"limits-fallback",
		"metadata-no-decode",
		"concurrent",
	}
	pass := 0
	for _, name := range checks {
		ok := run(name)
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}

func run(name string) bool {
	switch name {
	case "skeleton":
		return true
	default:
		return false
	}
}
