package wfg

import "errors"

var ErrCycle = errors.New("deadlock cycle detected")

type CycleError struct {
	Cycle []string
}

func (e CycleError) Error() string { return ErrCycle.Error() + ": " + join(e.Cycle) }
func (e CycleError) Unwrap() error { return ErrCycle }

func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " -> "
		}
		out += id
	}
	return out
}

type Graph struct {
	out map[string]string
}

func New() *Graph { return &Graph{out: map[string]string{}} }

func (g *Graph) Add(waiter, holder string) error {
	g.out[waiter] = holder
	if path, ok := g.cycle(waiter); ok {
		delete(g.out, waiter)
		return CycleError{Cycle: path}
	}
	return nil
}

func (g *Graph) Remove(waiter string) { delete(g.out, waiter) }

func (g *Graph) Holder(waiter string) (string, bool) {
	h, ok := g.out[waiter]
	return h, ok
}

func (g *Graph) HasCycle() bool {
	seen := map[string]int{}
	for id := range g.out {
		if seen[id] == 0 {
			path := []string{}
			on := map[string]bool{}
			cur := id
			for cur != "" {
				if seen[cur] == 1 {
					return false
				}
				if on[cur] {
					return true
				}
				on[cur] = true
				path = append(path, cur)
				cur = g.out[cur]
			}
			for _, p := range path {
				seen[p] = 1
			}
		}
	}
	return false
}

func (g *Graph) cycle(start string) ([]string, bool) {
	path := []string{start}
	seen := map[string]bool{start: true}
	cur := g.out[start]
	for cur != "" {
		path = append(path, cur)
		if cur == start {
			return path, true
		}
		if seen[cur] {
			return nil, false
		}
		seen[cur] = true
		cur = g.out[cur]
	}
	return nil, false
}
