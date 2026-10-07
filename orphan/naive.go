package orphan

import (
	"fmt"
	"sort"
)

// NaiveModel is an independently written reference implementation. It stores
// every object and every edge explicitly and, after every mutation, rebuilds
// each affected object's verdict by iterating ALL of its inbound edges. It
// deliberately shares no implementation with System (no helper reuse) so that
// agreement over long randomized histories is a meaningful differential test.
type NaiveModel struct {
	cfg   Config
	clock Clock
	objs  map[string]bool
	edges map[Edge]bool
	gen   map[string]Generation
	since map[string]int64
}

// NewNaive constructs the reference model from the same Config.
func NewNaive(cfg Config, clock Clock) (*NaiveModel, error) {
	if _, err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &NaiveModel{
		cfg:   cfg,
		clock: clock,
		objs:  map[string]bool{},
		edges: map[Edge]bool{},
		gen:   map[string]Generation{},
		since: map[string]int64{},
	}, nil
}

func (n *NaiveModel) AddObject(id string) bool {
	if n.objs[id] {
		return false
	}
	n.objs[id] = true
	n.recomputeAll()
	return true
}

func (n *NaiveModel) AddEdge(typ, src, dst string) error {
	if !n.objs[dst] {
		return fmt.Errorf("%w: %q", ErrObjectNotFound, dst)
	}
	if !n.objs[src] {
		return fmt.Errorf("%w: %q", ErrObjectNotFound, src)
	}
	if _, ok := n.cfg.Types[typ]; !ok {
		return fmt.Errorf("%w: %q", ErrTypeNotConfigured, typ)
	}
	n.edges[Edge{Type: typ, Src: src, Dst: dst}] = true
	n.recomputeAll()
	return nil
}

func (n *NaiveModel) RemoveEdge(typ, src, dst string) (bool, error) {
	if !n.objs[dst] {
		return false, fmt.Errorf("%w: %q", ErrObjectNotFound, dst)
	}
	if !n.objs[src] {
		return false, fmt.Errorf("%w: %q", ErrObjectNotFound, src)
	}
	if _, ok := n.cfg.Types[typ]; !ok {
		return false, fmt.Errorf("%w: %q", ErrTypeNotConfigured, typ)
	}
	e := Edge{Type: typ, Src: src, Dst: dst}
	if !n.edges[e] {
		return false, nil
	}
	delete(n.edges, e)
	n.recomputeAll()
	return true, nil
}

// Scan applies gen-1 promotion and gen-2 cleanup exactly as specified, using
// the same "elapsed >= grace" boundary semantics.
func (n *NaiveModel) Scan() {
	now := n.clock.NowMs()
	ids := make([]string, 0)
	for id := range n.objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !n.objs[id] {
			continue
		}
		switch n.gen[id] {
		case Gen1:
			if now-n.since[id] >= n.cfg.GraceGen1Ms {
				n.gen[id] = Gen2
				n.since[id] = now
			}
		case Gen2:
			if now-n.since[id] >= n.cfg.GraceGen2Ms {
				n.cleanup(id)
			}
		}
	}
}

// cleanup mirrors DefaultCascade: delete the object and every edge touching
// it, then recompute everyone.
func (n *NaiveModel) cleanup(id string) {
	delete(n.objs, id)
	delete(n.gen, id)
	delete(n.since, id)
	for e := range n.edges {
		if e.Src == id || e.Dst == id {
			delete(n.edges, e)
		}
	}
	n.recomputeAll()
}

// recomputeAll derives queue membership from scratch. Preserving an existing
// queue placement is the only continuity: a currently-queued orphan keeps its
// generation/timer; a retained object is non-orphan with no memory; a new
// orphan starts gen 1 now.
func (n *NaiveModel) recomputeAll() {
	now := n.clock.NowMs()
	for id := range n.objs {
		if n.retained(id) {
			n.gen[id] = NoGen
			n.since[id] = 0
			continue
		}
		if n.gen[id] == NoGen {
			n.gen[id] = Gen1
			n.since[id] = now
		}
	}
}

// retained is the deliberately naive verdict: gather inbound edges by
// scanning the entire edge set, then apply layer 1 then layer 2.
func (n *NaiveModel) retained(id string) bool {
	present := map[string]int{}
	for e := range n.edges {
		if e.Dst == id {
			present[e.Type]++
		}
	}
	// Layer 1: independent types.
	var indep []string
	for t, tc := range n.cfg.Types {
		if tc.Kind == Independent {
			indep = append(indep, t)
		}
	}
	sort.Strings(indep)
	for _, t := range indep {
		if present[t] > 0 {
			return true
		}
	}
	// Layer 2: joint components (union-find recomputed locally each time).
	for _, members := range n.jointComponents() {
		ok := true
		for _, m := range members {
			if present[m] == 0 {
				ok = false
				break
			}
		}
		if ok {
			for _, m := range members {
				for _, r := range n.cfg.Types[m].Requires {
					if n.cfg.Types[r].Kind == Independent && present[r] == 0 {
						ok = false
					}
				}
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (n *NaiveModel) jointComponents() [][]string {
	parent := map[string]string{}
	var joints []string
	for t, tc := range n.cfg.Types {
		if tc.Kind == Joint {
			parent[t] = t
			joints = append(joints, t)
		}
	}
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	for _, t := range joints {
		for _, r := range n.cfg.Types[t].Requires {
			if n.cfg.Types[r].Kind == Joint {
				ra, rb := find(t), find(r)
				if ra != rb {
					parent[rb] = ra
				}
			}
		}
	}
	comp := map[string][]string{}
	for _, t := range joints {
		r := find(t)
		comp[r] = append(comp[r], t)
	}
	var out [][]string
	for _, m := range comp {
		sort.Strings(m)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// StateOf mirrors System.StateOf.
func (n *NaiveModel) StateOf(id string) (State, bool) {
	if !n.objs[id] {
		return State{}, false
	}
	return State{Exists: true, Generation: n.gen[id], SinceMs: n.since[id]}, true
}

func (n *NaiveModel) pending(g Generation) []string {
	var out []string
	for id := range n.objs {
		if n.gen[id] == g {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (n *NaiveModel) PendingGen1() []string { return n.pending(Gen1) }
func (n *NaiveModel) PendingGen2() []string { return n.pending(Gen2) }

// Edges returns a sorted snapshot of all edges.
func (n *NaiveModel) Edges() []Edge {
	var out []Edge
	for e := range n.edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		return out[i].Dst < out[j].Dst
	})
	return out
}
