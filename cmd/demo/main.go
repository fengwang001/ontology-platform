// Command demo 演示可撤销批量重命名与冲突解析器的各项判定。
package main

import (
	"fmt"
	"os"

	"ontology/name"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("name: 空串合法/分隔符普通/非法UTF-8拒绝",
		name.Valid("") && name.Valid("a/b") && !name.Valid("\xff"))
	ns := name.New("a", "b")
	check("name: 基本改名与冲突拒绝",
		ns.Rename("a", "c") == nil && ns.Rename("c", "b") == name.ErrExist)
	fmt.Printf("TOTAL 2 checks, %d failed\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
