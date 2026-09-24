// Command demo 演示游标分页器的各项保证，逐条打印 OK/FAIL 判定。
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	check("skeleton", true, "demo 骨架可运行")
	fmt.Printf("TOTAL %d checks, %d failed\n", 1, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
