// Demo: 逐条演练通配模式的核心语义。不读参数、不联网。
package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	check("skeleton", true)

	if fails == 0 {
		fmt.Println("ALL OK")
		return
	}
	fmt.Printf("%d FAIL\n", fails)
}
