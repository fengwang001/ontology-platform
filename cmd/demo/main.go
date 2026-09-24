// Command demo 逐项演示 logical/props 包的语义判定。
package main

import "fmt"

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d passed, %d failed\n", passed, failed)
}
