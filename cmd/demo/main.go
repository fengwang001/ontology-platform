package main

import "fmt"

// 判定项在后续各包实现后逐条补入 check()。

func main() {
	pass, total := checks()
	for _, line := range report {
		fmt.Println(line)
	}
	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}

var report []string

func checks() (pass, total int) {
	report = append(report, "OK skeleton runs")
	return 1, 1
}
