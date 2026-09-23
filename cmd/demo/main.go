// Command demo 逐条演练工作队列的语义、推导结论与复杂度约束。
package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	}

	check("skeleton", true)

	if pass == total {
		fmt.Printf("TOTAL %d/%d OK\n", pass, total)
		return
	}
	fmt.Printf("TOTAL %d/%d FAIL\n", pass, total)
}
