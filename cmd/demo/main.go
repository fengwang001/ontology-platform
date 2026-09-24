package main

import "fmt"

var pass, total int

func check(name string, ok bool) {
	total++
	if ok {
		pass++
		fmt.Println("OK   ", name)
	} else {
		fmt.Println("FAIL ", name)
	}
}

func main() {
	// 判定随实现进度逐条补充。
	fmt.Printf("TOTAL: %d/%d OK\n", pass, total)
	if pass != total {
		panic("demo failed")
	}
}
