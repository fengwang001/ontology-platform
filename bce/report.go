package bce

import (
	"fmt"
	"strings"
)

// Decision is the verdict for one bounds check.
type Decision struct {
	ID      int
	Block   string
	Arr     string
	Idx     Operand
	Removed bool
	Reasons []string // kept checks only, from the fixed reason vocabulary
	Facts   []string // supporting facts with their sources
}

// Report is the deterministic analysis output for one input.
type Report struct {
	Program   string
	Decisions []Decision
	Removed   int
	Stats     Stats
}

// String renders the report. Rendering is fully deterministic: identical
// inputs produce byte-identical output.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "program %s: checks=%d removed=%d kept=%d\n",
		r.Program, len(r.Decisions), r.Removed, len(r.Decisions)-r.Removed)
	for _, d := range r.Decisions {
		verdict := "KEEP"
		if d.Removed {
			verdict = "REMOVE"
		}
		fmt.Fprintf(&b, "check #%d %s[%s] @%s: %s\n", d.ID, d.Arr, d.Idx.String(), d.Block, verdict)
		if len(d.Reasons) > 0 {
			fmt.Fprintf(&b, "  reasons: %s\n", strings.Join(d.Reasons, "; "))
		}
		facts := "(none)"
		if len(d.Facts) > 0 {
			facts = strings.Join(d.Facts, "; ")
		}
		fmt.Fprintf(&b, "  facts: %s\n", facts)
	}
	return b.String()
}
