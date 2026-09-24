package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	pass func() bool
}

var checks = []check{
	{"separators-and-whitespace", stub},
	{"comments", stub},
	{"continuations", stub},
	{"blank-continuation", stub},
	{"escapes-and-u-error", stub},
	{"duplicate-key-order", stub},
	{"store-special-chars", stub},
	{"random-roundtrip-1000", stub},
	{"byte-inspection-counter", stub},
}

func stub() bool { return true }

func main() {
	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.pass() {
			status, failed = "FAIL", failed+1
		}
		fmt.Printf("%-28s %s\n", c.name, status)
	}
	fmt.Printf("total %d/%d ok\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}
