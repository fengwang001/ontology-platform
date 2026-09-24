package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"分隔符与空白", false},
		{"注释", false},
		{"续行四样例", false},
		{"空白续行", false},
		{"转义", false},
		{"\\u 错误行列号", false},
		{"重复键顺序", false},
		{"回写特殊字符", false},
		{"1000 组往返", false},
		{"计数器 ≤2N", false},
	}

	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(checks))
}
