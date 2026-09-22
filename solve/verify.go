package solve

import (
	"errors"
	"fmt"

	"ontology/rng"
	"ontology/ver"
)

// VerifyError names the first constraint that a proposed solution violates.
type VerifyError struct {
	Edge     Edge
	Actual   string // selected version of Edge.Pkg ("" if absent)
	Selected bool
}

func (e *VerifyError) Error() string {
	if !e.Selected {
		return "solve: verify: " + e.Edge.String() + " but no version was selected"
	}
	return fmt.Sprintf("solve: verify: %s but selected %s@%s",
		e.Edge.String(), e.Edge.Pkg, e.Actual)
}

// Verify independently checks every effective constraint against solution.
// Roots must be satisfied, and every constraint declared by a selected
// package@version must be satisfied by the selected version of its target.
func (s *Solver) Verify(roots []Root, solution map[string]string) error {
	for _, root := range roots {
		v, ok := solution[root.Pkg]
		if err := checkEdge(rootEdge(root), v, ok); err != nil {
			return err
		}
	}
	for pkg, version := range solution {
		for _, d := range s.g.Deps(pkg, version) {
			v, ok := solution[d.Pkg]
			e := Edge{From: pkg, FromVer: version, Pkg: d.Pkg, Spec: d.Spec}
			if err := checkEdge(e, v, ok); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkEdge(e Edge, selected string, ok bool) error {
	if !ok {
		return &VerifyError{Edge: e, Selected: false}
	}
	r, err := parseEdge(e)
	if err != nil {
		return err
	}
	pv, err := ver.Parse(selected)
	if err != nil {
		return err
	}
	if !r.Contains(pv) {
		return &VerifyError{Edge: e, Actual: selected, Selected: true}
	}
	return nil
}

var errBadEdgeSpec = errors.New("solve: invalid constraint in verification")

func parseEdge(e Edge) (rng.Range, error) {
	r, err := rng.Parse(e.Spec)
	if err != nil {
		return rng.Range{}, errBadEdgeSpec
	}
	return r, nil
}
