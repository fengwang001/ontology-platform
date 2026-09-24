// Command demo 逐条验证 qp/qpline 包的关键语义，全部通过时退出码为 0。
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	fmt.Printf("total: %d failure(s)\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
