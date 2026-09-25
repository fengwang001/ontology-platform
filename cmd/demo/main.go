// Command demo runs the fair-queue scheduler acceptance checks: each
// check prints one OK/FAIL line, and the last line is the summary.
package main

import "fmt"

type check struct {
	name string
	run  func() (bool, string)
}

func main() {
	checks := []check{
		{"converge-1:3", checkConvergence},
		{"rejoin-no-monopoly", checkRejoin},
		{"tie-break-by-id", checkTieBreak},
		{"selection-comparisons", checkComparisons},
		{"fairness-100k", checkFairness},
		{"queue-cap-isolation", checkQueueCap},
		{"zero-cost-no-monopoly", checkZeroCost},
		{"concurrent-exactly-once", checkConcurrent},
		{"determinism-20-runs", checkDeterminism},
	}
	pass := 0
	for _, c := range checks {
		ok, detail := c.run()
		status := "OK  "
		if !ok {
			status = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s %s\n", status, c.name, detail)
	}
	fmt.Printf("TOTAL %d/%d checks passed\n", pass, len(checks))
}
