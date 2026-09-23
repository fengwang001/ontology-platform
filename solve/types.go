package solve

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/rng"
	"ontology/ver"
)

// Requirement is a root demand: Package must satisfy Constraint.
type Requirement struct {
	Package    string
	Constraint string
}

// Solution maps each reachable package to its single selected version.
type Solution map[string]ver.Version

// String renders the solution canonically (sorted by package), so equal
// solutions are byte-identical.
func (s Solution) String() string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s@%s", k, s[k])
	}
	return b.String()
}

// ConflictError reports unsatisfiability with a human-readable chain.
type ConflictError struct {
	Chain string
}

func (e *ConflictError) Error() string { return e.Chain }
func (e *ConflictError) Unwrap() error { return ErrNoSolution }

// imposed is one constraint applied to a package, with its provenance.
type imposed struct {
	pkg, ver string // source package@version; pkg == "" means root
	raw      string
	con      *rng.Constraint
}

func (im imposed) key() string { return im.pkg + "\x00" + im.ver + "\x00" + im.raw }

func (im imposed) source() string {
	if im.pkg == "" {
		return "root"
	}
	return im.pkg + "@" + im.ver
}

var errConflict = errors.New("solve: internal conflict")
