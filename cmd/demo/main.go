package main

import "fmt"

type check struct {
	name string
	fn   func() bool
}

func pending() bool { return false }

var checks = []check{
	{"三种分隔符与空白", pending},
	{"注释", pending},
	{"续行四个样例", pending},
	{"空白续行", pending},
	{"转义", pending},
	{"\\u错误行列号", pending},
	{"重复键顺序", pending},
	{"回写特殊字符", pending},
	{"1000组往返", pending},
	{"计数器", pending},
}

func main() {
	pass := 0
	for _, c := range checks {
		result := "FAIL"
		if c.fn() {
			result = "OK"
			pass++
		}
		fmt.Printf("%s: %s\n", c.name, result)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(checks))
}
