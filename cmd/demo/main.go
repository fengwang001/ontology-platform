package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	results = append(results,
		check{"skeleton", true},
	)

	pass := 0
	for _, r := range results {
		verdict := "OK"
		if !r.ok {
			verdict = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%-28s %s\n", r.name, verdict)
	}
	fmt.Printf("total %d/%d\n", pass, len(results))
}
