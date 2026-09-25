package main

import "fmt"

func main() {
	fail := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Printf("OK %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
			fail++
		}
	}

	check("skeleton", true)

	if fail == 0 {
		fmt.Println("TOTAL OK")
	} else {
		fmt.Printf("TOTAL FAIL %d\n", fail)
	}
}
