package main

import "fmt"

func main() {
	pass := 0
	total := 0

	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	check("skeleton", true)

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}
