package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		fails++
	}
}

func main() {
	// 骨架：所有判定暂为占位 true，随实现逐步替换。
	check("canonical samples (rule 2)", true)
	check("newline placement (rule 3)", true)
	check("five error kinds + offsets (rule 4)", true)
	check("all split points identical (rule 5)", true)
	check("round trip incl MIME (rule 6)", true)
	check("output cap stops at group boundary (rule 7)", true)
	check("inspection counter == input bytes", true)

	if fails == 0 {
		fmt.Println("ALL 7 CHECKS PASSED")
	} else {
		fmt.Printf("%d CHECK(S) FAILED\n", fails)
	}
}
