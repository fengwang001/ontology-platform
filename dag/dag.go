// Package dag implements an operator latency DAG with longest-path distances.
package dag

import (
	"errors"
	"maps"
	"slices"
	"sort"
	"sync/atomic"
)

var (
	ErrEmptyName = errors.New("dag: empty operator name")
	ErrDuplicate = errors.New("dag: duplicate operator")
	ErrUnknown   = errors.New("dag: unknown operator")
	ErrCycle     = errors.New("dag: edge would create a cycle")
	ErrNegative  = errors.New("dag: negative latency")
	ErrEndpoints = errors.New("dag: need exactly one source and one sink")
)

// Graph is an operator latency DAG; read-only methods are concurrency-safe.
type Graph struct {
	lat   map[string]int64
	adj   map[string][]string
	pre   map[string][]string
	indeg map[string]int
	relax atomic.Int64 // edge relaxations in the latest Longest run
}

func New() *Graph {
	g := &Graph{lat: map[string]int64{}, adj: map[string][]string{}, pre: map[string][]string{}, indeg: map[string]int{}}
	return g
}

func (g *Graph) Add(name string, lat int64) error {
	if name == "" {
		return ErrEmptyName
	}
	if lat < 0 {
		return ErrNegative
	}
	if _, ok := g.lat[name]; ok {
		return ErrDuplicate
	}
	g.lat[name], g.adj[name], g.pre[name], g.indeg[name] = lat, nil, nil, 0
	return nil
}

func (g *Graph) Link(from, to string) error {
	if _, ok := g.lat[from]; !ok {
		return ErrUnknown
	}
	if _, ok := g.lat[to]; !ok {
		return ErrUnknown
	}
	if from == to || g.reaches(to, from) {
		return ErrCycle
	}
	g.adj[from] = append(g.adj[from], to)
	g.pre[to] = append(g.pre[to], from)
	g.indeg[to]++
	return nil
}

func (g *Graph) reaches(a, b string) bool {
	seen := map[string]bool{a: true}
	stack := []string{a}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == b {
			return true
		}
		for _, m := range g.adj[n] {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	return false
}

func (g *Graph) Lat(name string) int64 { return g.lat[name] }

func (g *Graph) Next(name string) []string { return append([]string(nil), g.adj[name]...) }

func (g *Graph) Names() []string { return slices.Sorted(maps.Keys(g.lat)) }

func (g *Graph) Topo() []string {
	indeg := make(map[string]int, len(g.indeg))
	var queue, order []string
	for n, d := range g.indeg {
		indeg[n] = d
		if d == 0 {
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range g.adj[n] {
			if indeg[m]--; indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	return order
}

func (g *Graph) Endpoints() (source, sink string, err error) {
	ns, nk := 0, 0
	for _, n := range g.Names() {
		if g.indeg[n] == 0 {
			source, ns = n, ns+1
		}
		if len(g.adj[n]) == 0 {
			sink, nk = n, nk+1
		}
	}
	if ns != 1 || nk != 1 {
		return "", "", ErrEndpoints
	}
	return source, sink, nil
}

// Longest: dist(v) = max(dist(u)+lat(v)) in one topological pass; ties to smallest name.
func (g *Graph) Longest() (dist map[string]int64, pred map[string]string) {
	g.relax.Store(0)
	dist = make(map[string]int64, len(g.lat))
	pred = make(map[string]string, len(g.lat))
	for _, v := range g.Topo() {
		best, bp := int64(-1), ""
		pre := append([]string(nil), g.pre[v]...)
		sort.Strings(pre) // sorted: first max wins, ties to smallest name
		for _, u := range pre {
			g.relax.Add(1)
			if d := dist[u] + g.lat[v]; d > best {
				best, bp = d, u
			}
		}
		if bp == "" { // source: dist is its own latency
			best = g.lat[v]
		}
		dist[v], pred[v] = best, bp
	}
	return dist, pred
}
