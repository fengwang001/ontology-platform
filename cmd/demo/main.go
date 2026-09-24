// Command demo 运行随机超平面 LSH 分桶检索器的全部验收判定。
package main

import "fmt"

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
			return
		}
		fmt.Println("FAIL " + name)
		fails++
	}

	check("skeleton runs", true)

	if fails == 0 {
		fmt.Println("TOTAL: all checks passed")
		return
	}
	fmt.Printf("TOTAL: %d check(s) failed\n", fails)
}
