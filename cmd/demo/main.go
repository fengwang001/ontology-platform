package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"格式样例", false},
		{"往返", false},
		{"拒绝清单及偏移", false},
		{"大次数处理", false},
		{"组合字符不合并", false},
		{"所有切分点一致", false},
		{"计数器", false},
	}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("总计 %d/%d\n", len(checks)-fail, len(checks))
	if fail > 0 {
		os.Exit(1)
	}
}
