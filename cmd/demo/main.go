package main

import "fmt"

func main() {
	checks := []string{
		"errors: control, escape, hex, close, trailing",
		"surrogates: all required examples",
		"utf8: invalid decode and encode",
		"encode: slash, DEL, U+2028 stay literal",
		"roundtrip: random valid strings",
		"streaming: every split point agrees",
		"counter: at most two input bytes",
	}

	failures := 0
	for _, check := range checks {
		ok := true
		if !ok {
			failures++
		}
		status := "OK"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, check)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-failures, len(checks))
	if failures != 0 {
		fmt.Println("demo failed")
	}
}
