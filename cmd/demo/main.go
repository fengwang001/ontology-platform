package main

import "fmt"

// 判定项在后续实现各包时逐条补齐；骨架阶段先确保可运行。
func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
	}

	check("skeleton", true)
	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		panic("demo failed")
	}
}
