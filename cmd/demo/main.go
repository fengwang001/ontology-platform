// Command demo 逐条验证 rle 编解码器的语义，全部通过时退出码为 0。
package main

import (
	"fmt"
	"os"
)

func main() {
	checks := []struct {
		name string
		run  func() error
	}{
		// 随实现推进逐条补充。
	}
	ok := 0
	for _, c := range checks {
		if err := c.run(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			continue
		}
		fmt.Printf("OK   %s\n", c.name)
		ok++
	}
	fmt.Printf("total %d/%d\n", ok, len(checks))
	if ok != len(checks) {
		os.Exit(1)
	}
}
