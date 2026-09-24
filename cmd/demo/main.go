package main

import "fmt"

type check struct {
	name string
	fn   func() bool
}

func main() {
	checks := []check{
		{"separators & whitespace", chkSeparators},
		{"comments", chkComments},
		{"continuation samples", chkContinuation},
		{"blank continuation line", chkBlankCont},
		{"escapes & \\u error position", chkEscapes},
		{"duplicate key order", chkDupOrder},
		{"store special chars", chkStore},
		{"1000 random round trips", chkRandom},
		{"examined-byte counter", chkCounter},
	}
	fails := 0
	for _, c := range checks {
		if c.fn() {
			fmt.Printf("OK   %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
			fails++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fails, len(checks))
	if fails != 0 {
		fmt.Println("SOME CHECKS FAILED")
	}
}

func chkSeparators() bool   { return false }
func chkComments() bool     { return false }
func chkContinuation() bool { return false }
func chkBlankCont() bool    { return false }
func chkEscapes() bool      { return false }
func chkDupOrder() bool     { return false }
func chkStore() bool        { return false }
func chkRandom() bool       { return false }
func chkCounter() bool      { return false }
