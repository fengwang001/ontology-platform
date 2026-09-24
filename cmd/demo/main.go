// Command demo 逐条验证 RLE 编解码器的行为。
package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

var checks []check

func record(name string, ok bool) {
	checks = append(checks, check{name, ok})
}

func main() {
	// 骨架：各项检查随实现逐条补齐。
	record("format examples", false)

	pass := 0
	for _, c := range checks {
		tag := "FAIL"
		if c.ok {
			tag = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
