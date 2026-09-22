// Command demo exercises every semantic of the range-response assembler
// and prints one OK/FAIL line per check plus a final tally. It reads no
// arguments, touches no files and no network.
package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	run  func() (string, error) // returns extra info for the OK line
}

func main() {
	checks := []check{
		{"three range forms + clipping", checkForms},
		{"bytes=-0 unsatisfiable (with size)", checkMinusZero},
		{"syntax vs unsatisfiable distinguishable", checkErrorKinds},
		{"merge preserves byte set (exhaustive)", checkByteSet},
		{"comparison count n=100 vs n=10000", checkComplexity},
		{"short reads are filled", checkShortRead},
		{"all write split points identical", checkSplitPoints},
		{"single range is bare bytes", checkBareBytes},
		{"boundary avoids payload content", checkBoundary},
		{"three limits reject, state untouched", checkLimits},
		{"queries stable and pure", checkQueries},
	}
	failed := 0
	for _, c := range checks {
		info, err := c.run()
		if err != nil {
			failed++
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			continue
		}
		if info != "" {
			info = " (" + info + ")"
		}
		fmt.Printf("OK   %s%s\n", c.name, info)
	}
	fmt.Printf("TOTAL %d/%d OK\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}
