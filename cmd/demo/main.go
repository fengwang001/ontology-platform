// Command demo exercises the chunked streaming pipeline end to end and
// prints one OK/FAIL line per semantic guarantee.
package main

import (
	"fmt"
	"os"
)

func main() {
	checks := []struct {
		name string
		run  func() error
	}{
		{"zero-writes-no-chunk", checkZeroWrites},
		{"close-idempotent", checkCloseIdempotent},
		{"short-write-splits", checkShortWrites},
		{"backpressure-keeps-data", checkBackpressure},
		{"disconnect-resume", checkDisconnectResume},
		{"chunk-seq-call-agnostic", checkChunkSeq},
		{"ext-escaping-roundtrip", checkExtEscaping},
		{"limits-reject-cleanly", checkLimits},
		{"identity-under-concurrency", checkConcurrency},
		{"scan-bytes-independent-of-N", checkScanBytes},
	}
	failed := 0
	for _, c := range checks {
		if err := c.run(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			failed++
		} else {
			fmt.Printf("OK   %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}
