package solve

import (
	"fmt"

	"ontology/graph"
	"ontology/rng"
)

// Validate independently checks that sol satisfies every active
// constraint: all root requirements, all constraints declared by each
// selected package@version, exactly one version per package (guaranteed
// by the map shape), and no packages unreachable from the roots.
func Validate(g *graph.Graph, roots []Requirement, sol Solution) error {
	for pkg, v := range sol {
		found := false
		for _, rv := range g.Versions(pkg) {
			if rv.Compare(v) == 0 {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: %s@%s is not registered", ErrInvalidSolution, pkg, v)
		}
	}
	reachable := map[string]bool{}
	var queue []string
	for _, r := range roots {
		con, err := rng.Parse(r.Constraint)
		if err != nil {
			return err
		}
		v, ok := sol[r.Package]
		if !ok {
			return fmt.Errorf("%w: root package %q has no version", ErrInvalidSolution, r.Package)
		}
		if !con.Contains(v) {
			return fmt.Errorf("%w: root requires %s %s but solution has %s",
				ErrInvalidSolution, r.Package, r.Constraint, v)
		}
		if !reachable[r.Package] {
			reachable[r.Package] = true
			queue = append(queue, r.Package)
		}
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for _, d := range g.Dependencies(pkg, sol[pkg].String()) {
			if !g.Has(d.Target) {
				return fmt.Errorf("%w: %q (required by %s@%s)", ErrUnknownPackage, d.Target, pkg, sol[pkg])
			}
			tv, ok := sol[d.Target]
			if !ok {
				return fmt.Errorf("%w: %s@%s requires %s but it is not in the solution",
					ErrInvalidSolution, pkg, sol[pkg], d.Target)
			}
			if !d.Con.Contains(tv) {
				return fmt.Errorf("%w: %s@%s requires %s %q but solution has %s@%s",
					ErrInvalidSolution, pkg, sol[pkg], d.Target, d.Con, d.Target, tv)
			}
			if !reachable[d.Target] {
				reachable[d.Target] = true
				queue = append(queue, d.Target)
			}
		}
	}
	for pkg := range sol {
		if !reachable[pkg] {
			return fmt.Errorf("%w: %q is not reachable from any root", ErrInvalidSolution, pkg)
		}
	}
	return nil
}
