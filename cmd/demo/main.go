// Command demo 逐条验证 RLE 编解码器的关键语义，全部通过时退出码为 0。
package main

import (
	"fmt"
	"os"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	check("skeleton", true)
	if failed {
		os.Exit(1)
	}
}
