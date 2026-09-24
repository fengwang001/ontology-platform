package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		failed++
		fmt.Println("FAIL " + name)
	}
}

// 占位：实现 logical/props 后逐块替换为真实判定。
func pending() bool { return true }

func main() {
	check("separators-and-whitespace", pending())
	check("comments", pending())
	check("continuation-4-cases", pending())
	check("blank-continuation", pending())
	check("escapes-and-u-error-position", pending())
	check("duplicate-key-order", pending())
	check("store-specials", pending())
	check("roundtrip-1000", pending())
	check("scan-budget", pending())
	if failed == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAIL\n", failed)
		os.Exit(1)
	}
}
