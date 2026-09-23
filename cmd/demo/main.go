// Command demo runs end-to-end acceptance checks for the qp packages.
package main

import (
	"bytes"
	"fmt"

	"ontology/qp"
)

type check struct {
	name string
	fn   func() bool
}

func main() {
	checks := []check{
		{"trailing-ws-3", checkTrailingWS},
		{"no-split-=XX", checkNoSplit},
		{"76-limit", checkLimit},
		{"5-errors-offset", checkErrors},
		{"all-splits", checkSplits},
		{"roundtrip", checkRoundtrip},
		{"minimal-escape", checkMinimal},
		{"check-counter", checkCounter},
	}
	pass := 0
	for _, c := range checks {
		ok := c.fn()
		if ok {
			pass++
		}
		status := "FAIL"
		if ok {
			status = "OK"
		}
		fmt.Printf("%-18s %s\n", c.name, status)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}

func checkTrailingWS() bool { return false }

func checkNoSplit() bool { return false }

func checkLimit() bool {
	line := bytes.Repeat([]byte{'a'}, 76)
	return len(qp.Encode(line)) > 76
}

func checkErrors() bool { return false }

func checkSplits() bool { return false }

func checkRoundtrip() bool { return false }

func checkMinimal() bool { return false }

func checkCounter() bool { return false }
