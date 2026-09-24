// Package api is the public facade over prop: build, apply, read, self-check.
package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"

	"ontology/dag"
	"ontology/prop"
)

type Def = dag.Def
type Change = prop.Change

var (
	ErrInvalidDef  = dag.ErrInvalidDef
	ErrUnknownNode = dag.ErrUnknownNode
	ErrCycle       = dag.ErrCycle
	ErrNotSource   = prop.ErrNotSource
)

// API is a glitch-free derived-view store. Safe for concurrent use.
type API struct{ eng *prop.Engine }

func New(defs []Def) (*API, error) {
	g, err := dag.New(defs)
	if err != nil {
		return nil, err
	}
	return &API{eng: prop.New(g)}, nil
}

func (a *API) Apply(sets map[string]int64) ([]Change, error) { return a.eng.Apply(sets) }
func (a *API) Value(name string) (int64, bool)               { return a.eng.Value(name) }
func (a *API) View() map[string]int64                        { return a.eng.View() }

// naive recomputes every derived node from the source values in vals.
func naive(g *dag.Graph, vals map[string]int64) map[string]int64 {
	out := maps.Clone(vals)
	for lv := 1; lv <= g.MaxLevel; lv++ {
		for n, d := range g.Defs {
			if g.Levels[n] != lv {
				continue
			}
			switch d.Kind {
			case "scale":
				out[n] = d.K * out[d.Inputs[0]]
			case "add":
				out[n] = out[d.Inputs[0]] + d.K
			case "sum":
				var s int64
				for _, in := range d.Inputs {
					s += out[in]
				}
				out[n] = s
			}
		}
	}
	return out
}

// checkLog verifies inv2: unique entries, old/new match prev/cur, inputs first.
func checkLog(g *dag.Graph, log []Change, prev, cur map[string]int64) error {
	pos := map[string]int{}
	for i, c := range log {
		if _, dup := pos[c.Name]; dup {
			return fmt.Errorf("inv2: %q logged twice", c.Name)
		}
		pos[c.Name] = i
		if prev[c.Name] != c.Old || cur[c.Name] != c.New {
			return fmt.Errorf("inv2: %q old/new mismatch", c.Name)
		}
	}
	for i, c := range log {
		for _, in := range g.Defs[c.Name].Inputs {
			if j, ok := pos[in]; ok && j > i {
				return fmt.Errorf("inv2: %q logged before its input %q", c.Name, in)
			}
		}
	}
	return nil
}

var seven = []Def{
	{Name: "A", Kind: "src", K: 1}, {Name: "E", Kind: "src", K: 100},
	{Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2},
	{Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
	{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}},
	{Name: "F", Kind: "sum", Inputs: []string{"D", "E"}},
	{Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
}

var chain = []Def{
	{Name: "s", Kind: "src", K: 3}, {Name: "t", Kind: "src", K: 7},
	{Name: "n1", Kind: "add", Inputs: []string{"s"}, K: 1},
	{Name: "n2", Kind: "scale", Inputs: []string{"n1"}, K: 5},
	{Name: "n3", Kind: "sum", Inputs: []string{"n2", "t"}},
}

// SelfCheck verifies the four invariants on built-in graphs and op sequences.
func (a *API) SelfCheck() error {
	for gi, defs := range [][]Def{seven, chain} {
		g, err := dag.New(defs)
		if err != nil {
			return err
		}
		eng := prop.New(g)
		var srcs []string
		for n, d := range g.Defs {
			if d.Kind == "src" {
				srcs = append(srcs, n)
			}
		}
		rng := rand.New(rand.NewSource(int64(gi) + 1))
		for step := 0; step < 100; step++ {
			prev := eng.View()
			sets := map[string]int64{}
			for _, s := range srcs {
				if rng.Intn(2) == 0 {
					sets[s] = int64(rng.Intn(21) - 10)
				}
			}
			log, err := eng.Apply(sets)
			if err != nil {
				return err
			}
			cur := eng.View()
			if !maps.Equal(cur, naive(g, cur)) {
				return fmt.Errorf("inv1: graph %d step %d diverges from full recompute", gi, step)
			}
			if err := checkLog(g, log, prev, cur); err != nil {
				return fmt.Errorf("graph %d step %d: %w", gi, step, err)
			}
		}
		noop, err := eng.Apply(map[string]int64{srcs[0]: eng.View()[srcs[0]]})
		if err != nil || len(noop) != 0 {
			return fmt.Errorf("inv3: setting current value must yield an empty log")
		}
		before := eng.View()
		_, e1 := eng.Apply(map[string]int64{"nope": 1})
		_, e2 := eng.Apply(map[string]int64{srcs[0]: 9, "nope": 1})
		if !errors.Is(e1, ErrUnknownNode) || !errors.Is(e2, ErrUnknownNode) ||
			!maps.Equal(before, eng.View()) {
			return fmt.Errorf("inv4: rejected apply must fail cleanly and leave no trace")
		}
	}
	return nil
}
