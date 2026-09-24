package main

import (
	"fmt"
	"os"

	"ontology/win"
)

var failed bool

func check(name string, ok bool) {
	line := "OK"
	if !ok {
		line = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, line)
}

func main() {
	// 负时间戳窗口归属：[-10,0)、[0,10)、[-20,-10)
	neg := win.Index(-1, 10) == -1 && win.Start(-1, 10) == -10 && win.End(-1, 10) == 0 &&
		win.Index(-10, 10) == -1 && win.Index(-11, 10) == -2 && win.Index(0, 10) == 0
	check("负时间戳窗口归属", neg)
	if failed {
		os.Exit(1)
	}
}
