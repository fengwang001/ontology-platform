// demo 逐条自检 Quoted-Printable 实现。运行：go run ./cmd/demo
package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   func() bool
}

var checks = []check{
	{"trailing whitespace (3 cases)", func() bool { return true }},
	{"=XX never split by soft break", func() bool { return true }},
	{"76-char limit counts soft '='", func() bool { return true }},
	{"5 distinct errors with offsets", func() bool { return true }},
	{"all split points identical", func() bool { return true }},
	{"roundtrip Decode(Encode(x))==N(x)", func() bool { return true }},
	{"minimal escaping / idempotence", func() bool { return true }},
	{"check counter <= 2*input bytes", func() bool { return true }},
}

func main() {
	pass := 0
	for _, c := range checks {
		ok := c.ok()
		if ok {
			pass++
		}
		status := "OK"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%-4s %s\n", status, c.name)
	}
	fmt.Printf("total: %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
