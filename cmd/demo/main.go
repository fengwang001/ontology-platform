package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"严格样例与空输入", false},
		{"换行位置", false},
		{"五类错误可区分", false},
		{"错误偏移", false},
		{"所有切分点一致", false},
		{"往返(含MIME)", false},
		{"输出上限", false},
		{"检查计数==输入字节", false},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%-22s %s\n", c.name, status)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks pending")
	}
}
