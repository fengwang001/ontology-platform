// Command demo 演示布隆过滤器的基本语义判定。
package main

import "fmt"

var passed, total int

func judge(ok bool, msg string) {
	total++
	if ok {
		passed++
		fmt.Println("OK", msg)
	} else {
		fmt.Println("FAIL", msg)
	}
}

func main() {
	judge(true, "skeleton runs")
	fmt.Printf("OK total: %d/%d passed\n", passed, total)
}
