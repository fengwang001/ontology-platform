package main

import "fmt"

func main() {
	pass := 0
	fail := 0
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK  ", name)
		} else {
			fail++
			fmt.Println("FAIL", name)
		}
	}

	check("skeleton", true)
	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
	if fail > 0 {
		panic("demo failed")
	}
}
