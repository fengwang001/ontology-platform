package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}

	check("demo skeleton", true)

	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}
