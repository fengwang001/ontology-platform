// Command demo 逐条打印本系统各关键性质的 OK/FAIL 判定。
package main

import (
	"fmt"

	"ontology/win"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	neg := win.Of(-1, 10) == (win.Window{Start: -10, End: 0}) &&
		win.Of(-10, 10) == (win.Window{Start: -10, End: 0}) &&
		win.Of(-11, 10) == (win.Window{Start: -20, End: -10})
	ok("negative-timestamp windows floor-divide", neg)
}
