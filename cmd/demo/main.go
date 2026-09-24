package main

import "fmt"

func main() {
	var pass, total int
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		status := "FAIL"
		if ok {
			status = "OK"
		}
		fmt.Printf("%-24s %s\n", name, status)
	}

	check("format-examples", false)
	check("roundtrip", false)
	check("reject-explicit-one", false)
	check("reject-zero", false)
	check("reject-leading-zero", false)
	check("reject-adjacent-same", false)
	check("reject-bad-escape", false)
	check("reject-trailing-slash", false)
	check("reject-missing-symbol", false)
	check("reject-invalid-utf8", false)
	check("large-count", false)
	check("combining-not-merged", false)
	check("all-splits", false)
	check("byte-counter", false)

	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}
