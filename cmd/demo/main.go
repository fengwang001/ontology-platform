package main

import "fmt"

func main() {
	pass, fail := 0, 0
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK  " + name)
		} else {
			fail++
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton runnable", true)

	total := pass + fail
	fmt.Printf("TOTAL %d/%d OK (%d FAIL)\n", pass, total, fail)
	if fail != 0 {
		panic("demo checks failed")
	}
}
