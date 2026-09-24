// 演示程序：逐条自检严格 JSON 字符串编解码器。
package main

import (
	"fmt"
	"os"

	"ontology/jstr"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{}

	// 骨架自检：包可被引用。
	_ = jstr.NewDecoder()
	checks = append(checks, check{"skeleton", true})

	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d\n", len(checks)-fail, len(checks))
	if fail > 0 {
		os.Exit(1)
	}
}
