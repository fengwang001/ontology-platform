package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton runnable", true)

	if pass == total {
		fmt.Printf("TOTAL %d/%d PASS\n", pass, total)
	} else {
		fmt.Printf("TOTAL %d/%d FAIL\n", pass, total)
	}
}
