package main

import "fmt"

func check(name string, ok bool) int {
	if ok {
		fmt.Println("OK   " + name)
		return 0
	}
	fmt.Println("FAIL " + name)
	return 1
}

func main() {
	failed := 0
	failed += check("skeleton", true)
	if failed == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAIL\n", failed)
	}
}
