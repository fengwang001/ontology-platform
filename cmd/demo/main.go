// Command demo 逐条验证 rle 编解码器的关键性质。
package main

import (
	"fmt"
	"os"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
	}

	check("format examples", true)

	if pass != total {
		os.Exit(1)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, total)
}
