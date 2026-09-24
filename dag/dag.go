// Package dag validates node definitions, detects cycles, computes
// levels and the consumer (downstream) table. It depends on nothing.
package dag

import (
	"errors"
	"fmt"
	"sort"
)

// Def describes one node. Kind is one of src/scale/add/sum.
type Def struct {
	Name   string
	Kind   string
	Inputs []string
	K      int64
}

var (
	ErrInvalidDef  = errors.New("dag: invalid definition")
	ErrUnknownNode = errors.New("dag: unknown node")
	ErrCycle       = errors.New("dag: dependency cycle")
)

// Graph is the validated, immutable structure of a view DAG.
type Graph struct {
	Defs      map[string]Def
	Levels    map[string]int
	Consumers map[string][]string // downstream nodes, in definition order
	MaxLevel  int
}

func arityOK(d Def) bool {
	switch d.Kind {
	case "src":
		return len(d.Inputs) == 0
	case "scale", "add":
		return len(d.Inputs) == 1
	case "sum":
		return len(d.Inputs) >= 1
	}
	return false
}

// New checks, in order: invalid definitions, unknown references, cycles.
// It reports only the first failure category encountered.
func New(defs []Def) (*Graph, error) {
	g := &Graph{
		Defs:      map[string]Def{},
		Levels:    map[string]int{},
		Consumers: map[string][]string{},
	}
	for _, d := range defs {
		if d.Name == "" || !arityOK(d) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidDef, d.Name)
		}
		if _, dup := g.Defs[d.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate name %q", ErrInvalidDef, d.Name)
		}
		seen := map[string]bool{}
		for _, in := range d.Inputs {
			if seen[in] {
				return nil, fmt.Errorf("%w: duplicate input %q", ErrInvalidDef, d.Name)
			}
			seen[in] = true
		}
		g.Defs[d.Name] = d
	}
	for _, d := range defs {
		for _, in := range d.Inputs {
			if _, ok := g.Defs[in]; !ok {
				return nil, fmt.Errorf("%w: %q", ErrUnknownNode, in)
			}
		}
	}
	const (
		white, gray, black = 0, 1, 2
	)
	color := map[string]int{}
	var visit func(n string) error
	visit = func(n string) error {
		color[n] = gray
		lv := 0
		for _, in := range g.Defs[n].Inputs {
			switch color[in] {
			case gray:
				return fmt.Errorf("%w: at %q", ErrCycle, in)
			case white:
				if err := visit(in); err != nil {
					return err
				}
			}
			if g.Levels[in]+1 > lv {
				lv = g.Levels[in] + 1
			}
		}
		g.Levels[n] = lv
		if lv > g.MaxLevel {
			g.MaxLevel = lv
		}
		color[n] = black
		return nil
	}
	names := make([]string, 0, len(defs))
	for n := range g.Defs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if color[n] == white {
			if err := visit(n); err != nil {
				return nil, err
			}
		}
	}
	for _, d := range defs {
		for _, in := range d.Inputs {
			g.Consumers[in] = append(g.Consumers[in], d.Name)
		}
	}
	return g, nil
}
