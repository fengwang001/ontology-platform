// Command demo 逐条演示媒体类型内容协商器的语义判定。
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d failed\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
