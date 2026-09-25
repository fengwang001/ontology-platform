// Command demo 逐条演练解析器语义。
package main

import "fmt"

func main() {
	ok := true
	check := func(name string, pass bool) {
		if pass {
			fmt.Println("OK  ", name)
		} else {
			ok = false
			fmt.Println("FAIL", name)
		}
	}
	_ = check
	fmt.Println("demo skeleton")
	if !ok {
		fmt.Println("TOTAL: FAIL")
	}
}
