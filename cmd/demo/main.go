// Command demo exercises the jstr codec and prints OK/FAIL per check.
package main

import "fmt"

var failed int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("error: unescaped control + offset", false)
	check("error: unknown escape + offset", false)
	check("error: short \\u hex + offset", false)
	check("error: missing closing quote + offset", false)
	check("error: trailing bytes + offset", false)
	check("surrogates: all section-2 samples", false)
	check("invalid utf8: decode and encode", false)
	check("minimal escape: /, U+007F, U+2028 raw", false)
	check("roundtrip: Decode(Encode(s)) == s", false)
	check("splits: every cut point identical", false)
	check("counter: 1MB one-byte feeds <= 2x", false)
	fmt.Printf("total: 11 checks, %d failed\n", failed)
}
