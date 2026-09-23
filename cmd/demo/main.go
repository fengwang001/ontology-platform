package main

import "fmt"

func main() {
	ok := true
	report := func(name string, good bool) {
		if good {
			fmt.Println("OK  " + name)
		} else {
			ok = false
			fmt.Println("FAIL " + name)
		}
	}
	_ = report
	// 各包实现后在此逐条补判定。
	if ok {
		fmt.Println("ALL OK")
	}
}
