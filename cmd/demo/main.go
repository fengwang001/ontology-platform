package main

import "fmt"

func main() {
	total, failed := 0, 0
	check := func(name string, ok bool) {
		total++
		if !ok {
			failed++
		}
		status := "OK"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, name)
	}
	_ = check
	fmt.Printf("total %d\n", total)
	if failed > 0 {
		panic("checks failed")
	}
}
