// Command demo 逐条验证严格模式 Base64 编解码器的语义。
package main

import "fmt"

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	fmt.Printf("TOTAL %d FAIL\n", failed)
}
