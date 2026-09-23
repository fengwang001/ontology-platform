package main

import "fmt"

func main() {
	fail := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
		} else {
			fail++
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton", true)

	if fail > 0 {
		fmt.Printf("TOTAL FAIL %d\n", fail)
		return
	}
	fmt.Println("TOTAL OK")
}
