package main

import "fmt"

// 演示程序：每个用例输出一行 OK/FAIL，最后一行总计。
// 后续每实现一个包，就在这里补上该包对应的判定。
func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"骨架", true},
	}

	pass := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
