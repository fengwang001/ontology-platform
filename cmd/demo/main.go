// Command demo 逐条演示 rle 编解码器的语义判定，全部通过时退出码为 0。
package main

import (
	"fmt"
	"os"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	check("skeleton", true, "demo 骨架可运行，判定待实现")
	fmt.Printf("TOTAL %d FAIL\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
