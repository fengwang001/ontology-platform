package main

import "fmt"

// demo 在各包实现过程中逐条接入判定；最终输出见各 check 行。
func main() {
	fail := 0
	report := func(name string, ok bool) {
		if ok {
			fmt.Println("OK  " + name)
			return
		}
		fmt.Println("FAIL " + name)
		fail++
	}

	report("skeleton", true)

	if fail == 0 {
		fmt.Println("TOTAL: all checks passed")
		return
	}
	fmt.Printf("TOTAL: %d check(s) failed\n", fail)
}
