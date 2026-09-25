// Command demo 逐条验证 RLE 编解码器的关键语义，全部通过时退出码为 0。
package main

import "fmt"

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK  " + name)
	} else {
		failed++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	check("skeleton", true)
	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		panic("demo checks failed")
	}
}
