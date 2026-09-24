package main

import "fmt"

func main() {
	fail := 0
	check("skeleton", true, &fail)
	if fail > 0 {
		fmt.Println("TOTAL FAIL")
		return
	}
	fmt.Println("TOTAL OK")
}

func check(name string, ok bool, fail *int) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	*fail++
}
