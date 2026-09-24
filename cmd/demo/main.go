package main

import "fmt"

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// scan/style/api 的判定随各包实现逐步补入。
	report("skeleton", true)
}
