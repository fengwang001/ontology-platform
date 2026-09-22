package pipeline

import (
	"os"

	"ontology/budget"
)

// CrashPointFromEnv builds a Faults hook driven by environment variables
// for the subprocess crash harness:
//
//	ESORT_CRASH   = spill | merge | finalize
//	ESORT_TRUNC   = absolute byte cut applied to the run with id in
//	ESORT_TRUNC_RUN (optional; 0 applies to the final flush run)
func CrashPointFromEnv() *Faults {
	phase := os.Getenv("ESORT_CRASH")
	if phase == "" {
		return nil
	}
	return &Faults{
		CrashAfter: func(p string) bool {
			if p != phase && !(phase == "merge" && p == "merge-mid") {
				return false
			}
			os.Exit(77)
			return true
		},
	}
}

var _ = budget.ErrClosed
