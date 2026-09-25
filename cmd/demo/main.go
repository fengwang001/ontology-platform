package main

import (
	"fmt"
	"os"
)

func main() {
	checks := []struct {
		name string
		fn   func() bool
	}{
		{"zero-write-no-chunk", checkZero},
		{"close-idempotent", checkClose},
		{"short-write-points", checkShortWrites},
		{"backpressure-no-loss", checkBackpressure},
		{"disconnect-resume", checkResume},
		{"chunks-call-independent", checkChunkSeq},
		{"ext-escape-roundtrip", checkExts},
		{"limits-and-backpressure", checkLimits},
		{"identity-under-concurrency", checkIdentity},
		{"scan-bytes-not-linear", checkScan},
	}
	passed := 0
	for _, c := range checks {
		if c.fn() {
			passed++
			fmt.Printf("OK   %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
	if passed != len(checks) {
		os.Exit(1)
	}
}
