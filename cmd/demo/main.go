// Command demo 逐项演示头部集合的语义，每步一行 OK/FAIL，最后输出总计。
// 不读参数、不联网；全部通过时退出码为 0。
// 本文件只串流程，具体检查逻辑在 checks.go。
package main

import (
	"fmt"
	"os"
)

func main() {
	failed := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			failed++
		} else {
			fmt.Printf("OK   %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}
