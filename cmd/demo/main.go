// Command demo 演示只追加日志的段索引与范围回放。
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
	check("demo skeleton", true)
	if failed == 0 {
		fmt.Println("OK total: all checks passed")
	} else {
		fmt.Printf("FAIL total: %d check(s) failed\n", failed)
	}
}
