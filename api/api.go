// Package api 对外接口：New / Apply / Value / View / SelfCheck。依赖 prop。
package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"

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

type API struct {
	mu  sync.RWMutex
	g   *dag.Graph
	eng *prop.Engine
}

func New(defs []Def) (*API, error) {
	g, err := dag.New(defs)
	if err != nil {
		return nil, err
	}
	return &API{g: g, eng: prop.New(g)}, nil
}

func (a *API) Apply(sets map[string]int64) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.eng.Apply(sets)
}

func (a *API) Value(name string) (int64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.Value(name)
}

func (a *API) View() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.Snapshot()
}

// fullRecompute 朴素全量重算：以当前源节点值为底，按层级重算所有派生节点。
func (a *API) fullRecompute() map[string]int64 {
	out := a.View()
	names := a.g.Names()
	sort.Slice(names, func(i, j int) bool { return a.g.Level(names[i]) < a.g.Level(names[j]) })
	for _, n := range names {
		d := a.g.Def(n)
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
	return out
}

// SelfCheck 在内置图与操作序列上核验四条不变量，与共享状态无关，可并发调用。
func (a *API) SelfCheck() error {
	graph, err := New([]Def{
		{Name: "A", Kind: "src", K: 1}, {Name: "E", Kind: "src", K: 100},
		{Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2},
		{Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
		{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}},
		{Name: "F", Kind: "sum", Inputs: []string{"D", "E"}},
		{Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
	})
	if err != nil {
		return err
	}
	seq := []map[string]int64{{"A": 5}, {"A": 5}, {"A": 0, "E": 10}, {"E": 10}, {"A": -3, "E": 7}}
	for i, sets := range seq {
		pre := graph.View()
		log, err := graph.Apply(sets)
		if err != nil {
			return fmt.Errorf("selfcheck apply %d: %w", i, err)
		}
		post := graph.View()
		if err := checkLog(pre, post, log, graph.g); err != nil { // 不变量 2
			return fmt.Errorf("selfcheck log %d: %w", i, err)
		}
		full := graph.fullRecompute() // 不变量 1
		for n, v := range full {
			if post[n] != v {
				return fmt.Errorf("selfcheck full-recompute %d: %s=%d want %d", i, n, post[n], v)
			}
		}
	}
	if log, _ := graph.Apply(map[string]int64{"A": -3}); len(log) != 0 { // 不变量 3
		return errors.New("selfcheck: no-op apply returned non-empty log")
	}
	before := graph.View() // 不变量 4
	if _, err := graph.Apply(map[string]int64{"D": 1}); !errors.Is(err, ErrNotSource) {
		return errors.New("selfcheck: set derived not rejected")
	}
	if _, err := graph.Apply(map[string]int64{"ZZ": 1}); !errors.Is(err, ErrUnknownNode) {
		return errors.New("selfcheck: unknown key not rejected")
	}
	for n, v := range before {
		if got, _ := graph.Value(n); got != v {
			return fmt.Errorf("selfcheck: rejected apply changed %s", n)
		}
	}
	return nil
}

// checkLog 不变量 2：每节点至多一次；旧值=Apply 前、新值=Apply 后；排在其日志内输入之后。
func checkLog(pre, post map[string]int64, log []Change, g *dag.Graph) error {
	pos := map[string]int{}
	for i, c := range log {
		if _, dup := pos[c.Name]; dup {
			return fmt.Errorf("node %s appears twice", c.Name)
		}
		pos[c.Name] = i
		if c.Old != pre[c.Name] || c.New != post[c.Name] {
			return fmt.Errorf("node %s log (%d,%d) mismatch", c.Name, c.Old, c.New)
		}
	}
	for i, c := range log {
		for _, in := range g.Def(c.Name).Inputs {
			if j, ok := pos[in]; ok && j > i {
				return fmt.Errorf("node %s before its input %s", c.Name, in)
			}
		}
	}
	return nil
}
