// Command demo 逐条演示媒体类型内容协商器的语义判定。
package main

import "fmt"

var fails int

func check(name string, cond bool) {
	if cond {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	fails++
}

func main() {
	check("骨架可运行", true)
	fmt.Printf("总计: %d 项失败\n", fails)
}
