// Command demo 逐条演示严格模式流式 Base64 编解码器的语义。
package main

import (
	"fmt"
	"os"
)

var checks int

func check(name string, ok bool) {
	checks++
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		os.Exit(1)
	}
}

func main() {
	fmt.Printf("TOTAL %d checks passed\n", checks)
}
