package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

func main() {
	results := checks()
	pass := 0
	for _, r := range results {
		prefix := "FAIL"
		if r.ok {
			prefix = "OK  "
			pass++
		}
		fmt.Println(prefix + " " + r.name)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(results))
	if pass != len(results) {
		os.Exit(1)
	}
}
