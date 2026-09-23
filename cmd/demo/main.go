// Command demo 逐条演练通配匹配器的语义并打印 OK/FAIL。
package main

import "fmt"

var checks int

func ok(name string, cond bool) {
	checks++
	if cond {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	ok("skeleton", true)
	fmt.Printf("total %d checks\n", checks)
}
