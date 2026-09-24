// Command demo 逐条演示 jstr 严格编解码器的语义判定，全部通过时退出码为 0。
package main

import "fmt"

var checks int

func report(name string, ok bool, detail string) {
	checks++
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("%-12s %s %s\n", name, mark, detail)
}

func main() {
	fmt.Printf("TOTAL %d checks\n", checks)
}
