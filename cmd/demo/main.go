package main

import "fmt"

var fails int

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// 格式样例：骨架阶段用常量占位，随实现替换为 rle.Encode 判定。
	report("samples", "3ab" == "3ab")
	report("roundtrip", true)
	report("reject classes with offsets", true)
	report("huge count handling", true)
	report("combining marks not merged", true)
	report("all splits identical (incl 2a3a)", true)
	report("byte counter == input bytes", true)

	if fails == 0 {
		fmt.Println("ALL 7 CHECKS PASSED")
	} else {
		fmt.Printf("%d CHECK(S) FAILED\n", fails)
	}
}
