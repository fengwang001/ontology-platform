package defassign

import (
	"fmt"
	"sort"
	"strings"
)

// DiagKind is the kind of a diagnostic.
type DiagKind int

const (
	// DiagUnassignedRead: a read whose variable is not assigned on every
	// path that reaches it.
	DiagUnassignedRead DiagKind = iota
	// DiagDeadAssign: an assignment whose value is never read on any path
	// before the variable is reassigned.
	DiagDeadAssign
)

// Diagnostic is a single finding. Pos is the node ID of the offending
// statement (registration order = source position). Paths lists, for
// DiagUnassignedRead, the arriving paths on which the variable may be
// unassigned; it is empty for DiagDeadAssign.
type Diagnostic struct {
	Pos   int
	Kind  DiagKind
	Var   string
	Paths []string
}

// String renders the diagnostic deterministically.
func (d Diagnostic) String() string {
	switch d.Kind {
	case DiagUnassignedRead:
		return fmt.Sprintf("#%d read(%s): possibly unassigned on paths: %s",
			d.Pos, d.Var, strings.Join(d.Paths, " | "))
	default:
		return fmt.Sprintf("#%d assign(%s): never read", d.Pos, d.Var)
	}
}

// lessDiag orders diagnostics by position, then kind, then variable.
func lessDiag(a, b Diagnostic) bool {
	if a.Pos != b.Pos {
		return a.Pos < b.Pos
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Var < b.Var
}

// collectDiagnostics derives the sorted diagnostic list from the flow
// result. The output is deterministic for a given program.
func collectDiagnostics(reg *Registry, fr flowResult) []Diagnostic {
	var diags []Diagnostic
	for id := 0; id < reg.NumNodes(); id++ {
		st := fr.inState[id]
		if len(st) == 0 {
			continue // unreachable statements produce no diagnostics
		}
		n := reg.Node(id)
		switch n.Kind {
		case KindRead:
			seen := make(map[string]bool)
			var paths []string
			for _, s := range st {
				if _, ok := s.asg[n.Var]; ok {
					continue
				}
				p := renderPath(s.path)
				if !seen[p] {
					seen[p] = true
					paths = append(paths, p)
				}
			}
			if len(paths) > 0 {
				sort.Strings(paths)
				diags = append(diags, Diagnostic{
					Pos:   id,
					Kind:  DiagUnassignedRead,
					Var:   n.Var,
					Paths: paths,
				})
			}
		case KindAssign:
			if !fr.liveOut[id][n.Var] {
				diags = append(diags, Diagnostic{
					Pos:  id,
					Kind: DiagDeadAssign,
					Var:  n.Var,
				})
			}
		}
	}
	sort.Slice(diags, func(i, j int) bool { return lessDiag(diags[i], diags[j]) })
	return diags
}

// RenderDiags renders a diagnostic list as a single deterministic string.
func RenderDiags(diags []Diagnostic) string {
	var sb strings.Builder
	for _, d := range diags {
		sb.WriteString(d.String())
		sb.WriteByte('\n')
	}
	return sb.String()
}
