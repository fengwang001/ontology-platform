package main

import "fmt"

type check struct {
	name string
	fn   func() bool
}

func main() {
	checks := []check{
		{"分隔符与空白", checkSeparators},
		{"注释", checkComments},
		{"续行与空白续行", checkContinuation},
		{"转义与\\u错误行列", checkEscapes},
		{"重复键顺序", checkOrder},
		{"回写特殊字符", checkStore},
		{"1000组往返", checkRoundTrip},
		{"检查次数上界", checkCounter},
	}
	failed := 0
	for _, c := range checks {
		if c.fn() {
			fmt.Println("OK  ", c.name)
		} else {
			failed++
			fmt.Println("FAIL", c.name)
		}
	}
	fmt.Printf("总计 %d/%d 通过\n", len(checks)-failed, len(checks))
	if failed > 0 {
		panic("checks failed")
	}
}

func checkSeparators() bool  { return false }
func checkComments() bool    { return false }
func checkContinuation() bool { return false }
func checkEscapes() bool     { return false }
func checkOrder() bool       { return false }
func checkStore() bool       { return false }
func checkRoundTrip() bool   { return false }
func checkCounter() bool     { return false }
