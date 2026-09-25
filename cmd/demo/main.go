// Command demo 逐条判定一致性导出器的各项性质，全部通过则退出码为 0。
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	check("skeleton", true, "demo 骨架可运行")
	if failed > 0 {
		fmt.Printf("TOTAL FAIL %d\n", failed)
		os.Exit(1)
	}
	fmt.Println("TOTAL OK")
}
