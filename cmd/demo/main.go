package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"skeleton runs", true},
	}

	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	if failed > 0 {
		fmt.Printf("%d check(s) failed\n", failed)
		return
	}
	fmt.Println("all checks passed")
}
