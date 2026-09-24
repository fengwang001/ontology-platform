// Command demo 逐条演示 rle 编解码器的语义检查。
package main

import "fmt"

var checks []struct {
	name string
	run  func() bool
}

func main() {
	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.run() {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d passed\n", len(checks)-failed, len(checks))
	if failed > 0 {
		fmt.Println("RESULT FAIL")
	} else {
		fmt.Println("RESULT OK")
	}
}
