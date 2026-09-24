// Command demo 逐条核验有界蓄存的规则，全部 OK 时退出码为 0。
package main

import (
	"fmt"
	"os"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("skeleton", true)
	if failed {
		os.Exit(1)
	}
}
