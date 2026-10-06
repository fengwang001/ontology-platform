package bce

import (
	"fmt"
	"strings"
)

// Cause is the reason a bound could not be proven.
type Cause int

const (
	CauseNone Cause = iota
	// CauseArrReassigned: the array variable was reassigned, invalidating
	// its length and passed-check facts.
	CauseArrReassigned
	// CauseJoinLost: a needed fact held on some but not all incoming
	// paths and was dropped at the control-flow join.
	CauseJoinLost
	// CauseLoopKilled: an assignment inside the enclosing loop
	// invalidated the needed fact at the loop head.
	CauseLoopKilled
	// CauseOutOfScope: proving the bound would need facts beyond the
	// four allowed kinds (e.g. assuming a variable keeps its value
	// across an assignment, or monotonicity of a non-induction variable).
	CauseOutOfScope
)

func (c Cause) String() string {
	switch c {
	case CauseArrReassigned:
		return "array-reassigned"
	case CauseJoinLost:
		return "join-lost-fact"
	case CauseLoopKilled:
		return "loop-killed-fact"
	case CauseOutOfScope:
		return "out-of-scope-facts"
	}
	return "none"
}

// Side is the verdict for one bound (lower or upper) of one check.
type Side struct {
	Proven   bool
	Evidence []string // fact sources, when proven
	Cause    Cause    // why unproven, when not proven
}

// Decision is the verdict for one bounds check.
type Decision struct {
	CheckID string
	Removed bool
	Lower   Side
	Upper   Side
}

// Report lists every check's decision. It is deterministic: the same
// input program always renders byte-identical output.
type Report struct {
	Decisions []Decision
	Removed   int
	Kept      int
}

func (s Side) render(name string, sb *strings.Builder) {
	if s.Proven {
		fmt.Fprintf(sb, "  %s: PROVEN evidence=%s\n", name, strings.Join(s.Evidence, ", "))
	} else {
		fmt.Fprintf(sb, "  %s: UNPROVEN cause=%s\n", name, s.Cause)
	}
}

func (r *Report) String() string {
	var sb strings.Builder
	for _, d := range r.Decisions {
		if d.Removed {
			fmt.Fprintf(&sb, "check %s: REMOVE\n", d.CheckID)
		} else {
			fmt.Fprintf(&sb, "check %s: KEEP\n", d.CheckID)
		}
		d.Lower.render("lower", &sb)
		d.Upper.render("upper", &sb)
	}
	fmt.Fprintf(&sb, "summary: removed=%d kept=%d total=%d\n", r.Removed, r.Kept, r.Removed+r.Kept)
	return sb.String()
}
