// Command demo 运行列式段编码与谓词下推扫描器的端到端判定。
// 不读参数、不联网；每步打印一行 OK/FAIL，最后一行打印总计。
package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	fn   func() bool
}

var checks []check

func add(name string, fn func() bool) { checks = append(checks, check{name, fn}) }

func safe(fn func() bool) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return fn()
}

func main() {
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if safe(c.fn) {
			status = "OK"
			pass++
		}
		fmt.Printf("%-4s %s\n", status, c.name)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}

func init() {
	add("demo 骨架", func() bool { return true })
}
