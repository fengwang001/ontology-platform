package graph

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrUnknownDependency = errors.New("unknown dependency")
	ErrCycle             = errors.New("dependency cycle")
)

type CycleError struct {
	Path []string
}

func (e CycleError) Error() string {
	return fmt.Sprintf("%v: %v", ErrCycle, e.Path)
}

func (e CycleError) Is(target error) bool { return target == ErrCycle }

type UnknownDependencyError struct {
	From string
	To   string
}

func (e UnknownDependencyError) Error() string {
	return fmt.Sprintf("%v: %s -> %s", ErrUnknownDependency, e.From, e.To)
}

func (e UnknownDependencyError) Is(target error) bool { return target == ErrUnknownDependency }

type Graph struct {
	nodes map[string]struct{}
	out   map[string]map[string]struct{}
	in    map[string]map[string]struct{}
}

func New() *Graph {
	return &Graph{nodes: map[string]struct{}{}, out: map[string]map[string]struct{}{}, in: map[string]map[string]struct{}{}}
}

func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.out[id] = map[string]struct{}{}
	g.in[id] = map[string]struct{}{}
}

func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return UnknownDependencyError{from, to}
	}
	if _, ok := g.nodes[to]; !ok {
		return UnknownDependencyError{from, to}
	}
	g.out[from][to] = struct{}{}
	g.in[to][from] = struct{}{}
	return nil
}

func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (g *Graph) Out(id string) []string {
	ids := make([]string, 0, len(g.out[id]))
	for next := range g.out[id] {
		ids = append(ids, next)
	}
	sort.Strings(ids)
	return ids
}

func (g *Graph) InDegree(id string) int { return len(g.in[id]) }

func (g *Graph) HasEdge(from, to string) bool {
	_, ok := g.out[from][to]
	return ok
}

func (g *Graph) Validate() error {
	color := map[string]uint8{}
	parent := map[string]string{}
	for _, root := range g.Nodes() {
		if color[root] != 0 {
			continue
		}
		type frame struct{ id string; next int }
		color[root] = 1
		stack := []frame{{root, 0}}
		for len(stack) > 0 {
			at := &stack[len(stack)-1]
			neighbors := g.Out(at.id)
			if at.next == len(neighbors) {
				color[at.id] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			next := neighbors[at.next]
			at.next++
			if color[next] == 1 {
				path := []string{next}
				for cur := at.id; cur != next; cur = parent[cur] {
					path = append(path, cur)
				}
				for left, right := 1, len(path)-1; left < right; left, right = left+1, right-1 {
					path[left], path[right] = path[right], path[left]
				}
				path = append(path, next)
				return CycleError{Path: path}
			}
			if color[next] == 0 {
				parent[next] = at.id
				color[next] = 1
				stack = append(stack, frame{next, 0})
			}
		}
	}
	return nil
}

func (g *Graph) Layers() ([][]string, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	remaining := map[string]int{}
	ready := []string{}
	for _, id := range g.Nodes() {
		remaining[id] = g.InDegree(id)
		if remaining[id] == 0 {
			ready = append(ready, id)
		}
	}
	layers := [][]string{}
	seen := 0
	for len(ready) > 0 {
		sort.Strings(ready)
		layers = append(layers, append([]string(nil), ready...))
		next := []string{}
		for _, id := range ready {
			seen++
			for _, down := range g.Out(id) {
				remaining[down]--
				if remaining[down] == 0 {
					next = append(next, down)
				}
			}
		}
		ready = next
	}
	if seen != len(g.nodes) {
		return nil, CycleError{}
	}
	return layers, nil
}
