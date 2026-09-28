package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check
	results = append(results, check{"skeleton", true})

	fail := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%-28s %s\n", r.name, status)
	}
	fmt.Printf("total: %d/%d OK\n", len(results)-fail, len(results))
	if fail > 0 {
		fmt.Println("DEMO FAILED")
		return
	}
}
