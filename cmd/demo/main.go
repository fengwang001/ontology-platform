// Command demo 逐条演示并校验 rle 编解码器的语义，全部通过时退出码为 0。
package main

import "fmt"

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// 骨架：各判定随实现逐步补充。
	fmt.Printf("total %d failure(s)\n", failures)
}
