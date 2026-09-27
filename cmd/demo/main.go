package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check
	// 占位：每实现一个包补一条判定。
	results = append(results, check{"skeleton", true})

	pass := 0
	for _, r := range results {
		status := "FAIL"
		if r.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("demo failed")
	}
}
