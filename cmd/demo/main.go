package main

import "fmt"

// 演示程序：随各包实现逐条补全 check（见任务书第十节，共 13 项）。

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	check("skeleton", true)
}
