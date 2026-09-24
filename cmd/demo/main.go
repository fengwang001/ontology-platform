package main

import "fmt"

var pass, fail int

func check(ok bool, name, detail string) {
	if ok {
		pass++
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		fail++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func main() {
	check(true, "skeleton", "demo 骨架可运行")
	fmt.Printf("TOTAL %d passed, %d failed\n", pass, fail)
	if fail > 0 {
		panic("demo checks failed")
	}
}
