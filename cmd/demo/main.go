package main

import "fmt"

var fails int

func report(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		fails++
	}
}

func main() {
	// 各判定随 b64 / stream 实现逐步接入。
	report("skeleton", true)

	if fails == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d CHECK(S) FAILED\n", fails)
	}
}
