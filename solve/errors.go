package solve

import (
	"errors"
	"strings"
)

// ErrSearchBudget means the search exceeded its configured attempt budget.
// It is distinct from any no-solution condition.
var ErrSearchBudget = errors.New("solve: search budget exceeded")

// Edge is one hop in a conflict chain: From@FromVer declared Spec against Pkg.
type Edge struct {
	From    string
	FromVer string
	Pkg     string
	Spec    string
}

func (e Edge) String() string {
	if e.From == "" {
		return "root requires " + e.Pkg + " " + e.Spec
	}
	return e.From + "@" + e.FromVer + " requires " + e.Pkg + " " + e.Spec
}

// ConflictError explains unsolvability through the two incompatible chains.
type ConflictError struct {
	Pkg  string
	Low  []Edge // chain establishing the lower bound
	High []Edge // chain establishing the upper bound

	// NoCandidate means the interval itself was non-empty but no registered
	// version of Pkg falls inside it; Nearest is the closest available version.
	NoCandidate bool
	Nearest     string
}

func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString("solve: no solution: constraints on ")
	b.WriteString(e.Pkg)
	b.WriteString(" conflict\n  chain A: ")
	writeChain(&b, e.Low)
	if e.NoCandidate {
		b.WriteString("\n  chain B: no registered version satisfies it")
		if e.Nearest != "" {
			b.WriteString(" (nearest available: ")
			b.WriteString(e.Pkg)
			b.WriteString("@")
			b.WriteString(e.Nearest)
			b.WriteString(")")
		}
		return b.String()
	}
	b.WriteString("\n  chain B: ")
	writeChain(&b, e.High)
	return b.String()
}

func writeChain(b *strings.Builder, chain []Edge) {
	for i, e := range chain {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(e.String())
	}
}

// UnknownPackageError names a constraint target that was never registered.
type UnknownPackageError struct {
	Pkg    string
	DeclBy Edge
}

func (e *UnknownPackageError) Error() string {
	return "solve: constraint references unregistered package " + e.Pkg +
		" (from " + e.DeclBy.String() + ")"
}

// Is lets errors.Is(err, ErrSearchBudget) succeed for budget aborts.
func isBudget(err error) bool { return errors.Is(err, ErrSearchBudget) }
