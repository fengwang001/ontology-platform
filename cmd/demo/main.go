// Command demo 逐条打印采样剖析与热点归因器的验收判定。
package main

import "fmt"

var passed, failed int

func check(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	check(true, "skeleton ready")
	fmt.Printf("SUMMARY %d/%d OK\n", passed, passed+failed)
}
