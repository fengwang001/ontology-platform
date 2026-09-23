package main

import "fmt"

var pass, fail int

func check(name string, ok bool) {
	if ok {
		pass++
		fmt.Println("OK  ", name)
	} else {
		fail++
		fmt.Println("FAIL", name)
	}
}

func main() {
	check("skeleton", true)
	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
	if fail > 0 {
		panic("FAIL")
	}
}
