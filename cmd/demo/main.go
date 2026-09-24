// Command demo 逐条判定内容处置头部的解析、拼接与定名行为。
package main

import (
	"fmt"
	"os"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "ok"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s (%s)\n", status, name, detail)
}

func main() {
	check("skeleton", true, "demo harness runs")
	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
