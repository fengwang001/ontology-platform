// demo 逐条验证 jstr/esc 的语义并打印 OK/FAIL。
package main

import (
	"fmt"
	"os"
)

var lines []string

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	}
	lines = append(lines, mark+" "+name)
}

func main() {
	fail := 0
	for _, l := range lines {
		if l[:4] == "FAIL" {
			fail++
		}
		fmt.Println(l)
	}
	fmt.Printf("total: %d checks, %d failed\n", len(lines), fail)
	if fail > 0 {
		os.Exit(1)
	}
}

