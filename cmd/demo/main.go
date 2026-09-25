// Command demo 逐条演示熔断与舱壁保护器的关键判定。
package main

import "fmt"

var passed, failed int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failed++
	}
	passed++
	fmt.Printf("%s %s\n", verdict, name)
}

func main() {
	fmt.Println("OK demo skeleton")
}
