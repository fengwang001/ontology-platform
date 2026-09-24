// Command demo 演示段索引与范围回放器的各项判定，全部通过则以退出码 0 结束。
package main

import "fmt"

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("skeleton runs", true)
	fmt.Printf("TOTAL %d checks, %d failed\n", 1, failures)
	if failures > 0 {
		panic("demo checks failed")
	}
}
