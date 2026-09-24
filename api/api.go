// Package api is the public face of the operator latency DAG.
package api

import (
	"errors"
	"fmt"
	"ontology/crit"
	"ontology/dag"
)

// ErrSelfCheck: built-in invariant checks failed. Add/Link return dag's sentinels.
var ErrSelfCheck = errors.New("api: self-check failed")

type Pipeline struct{ g *dag.Graph }

func New() *Pipeline { return &Pipeline{g: dag.New()} }

func (p *Pipeline) Add(name string, lat int64) error { return p.g.Add(name, lat) }

func (p *Pipeline) Link(from, to string) error { return p.g.Link(from, to) }

func (p *Pipeline) EndToEnd() (int64, error) {
	_, total, err := crit.CriticalPath(p.g)
	return total, err
}

func (p *Pipeline) Bottleneck() (string, int64, error) { return crit.Bottleneck(p.g) }

func brute(g *dag.Graph) (int64, error) {
	source, sink, err := g.Endpoints()
	if err != nil {
		return 0, err
	}
	best := int64(-1)
	var dfs func(n string, sum int64)
	dfs = func(n string, sum int64) {
		sum += g.Lat(n)
		if n == sink {
			best = max(best, sum)
			return
		}
		for _, m := range g.Next(n) {
			dfs(m, sum)
		}
	}
	dfs(source, 0)
	return best, nil
}

func (p *Pipeline) SelfCheck() error {
	for i, q := range []*Pipeline{fiveNode(), chain(7), tieGraph()} {
		got, err := q.EndToEnd() // invariant 1: DP equals brute force
		want, _ := brute(q.g)
		if err != nil || got != want {
			return fmt.Errorf("%w: graph %d: EndToEnd %d, want %d (err %v)", ErrSelfCheck, i, got, want, err)
		}
		for _, err := range []error{checkBottleneck(q), checkDist(q.g), checkRejected(q)} { // invariants 2-4
			if err != nil {
				return fmt.Errorf("%w: graph %d: %v", ErrSelfCheck, i, err)
			}
		}
	}
	return nil
}

func checkBottleneck(q *Pipeline) error {
	name, lat, err := q.Bottleneck()
	if err != nil {
		return err
	}
	path, _, _ := crit.CriticalPath(q.g)
	onPath, maxLat, first := false, int64(-1), ""
	for _, n := range path {
		onPath = onPath || n == name
		if l := q.g.Lat(n); l > maxLat || (l == maxLat && (first == "" || n < first)) {
			maxLat, first = l, n
		}
	}
	if !onPath || lat != maxLat || name != first {
		return fmt.Errorf("bottleneck %s(%d) off path or not max", name, lat)
	}
	return nil
}

func checkDist(g *dag.Graph) error {
	dist, pred := g.Longest()
	for _, v := range g.Names() {
		if pred[v] != "" && dist[v] != dist[pred[v]]+g.Lat(v) {
			return fmt.Errorf("dist(%s) inconsistent", v)
		}
	}
	return nil
}

func checkRejected(q *Pipeline) error {
	before, _ := q.EndToEnd()
	names := fmt.Sprint(q.g.Names())
	source, sink, _ := q.g.Endpoints()
	bad := []error{q.Add(source, 1), q.Add("neg", -1), q.Link(source, "ghost"),
		q.Link(sink, source), q.Link(source, source)} // dup, negative, unknown, cycle, self-loop
	for _, err := range bad {
		if err == nil {
			return errors.New("rejected op returned nil error")
		}
	}
	after, _ := q.EndToEnd()
	if before != after || names != fmt.Sprint(q.g.Names()) {
		return errors.New("rejected op changed state")
	}
	return nil
}

type op struct {
	name string
	lat  int64
}

func build(ops []op, edges [][2]string) *Pipeline {
	p := New()
	for _, o := range ops {
		_ = p.Add(o.name, o.lat)
	}
	for _, e := range edges {
		_ = p.Link(e[0], e[1])
	}
	return p
}

func fiveNode() *Pipeline {
	return build([]op{{"S", 0}, {"A", 30}, {"A2", 30}, {"B", 50}, {"T", 0}},
		[][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}})
}

func tieGraph() *Pipeline {
	return build([]op{{"S", 5}, {"M1", 10}, {"M2", 10}, {"T", 5}},
		[][2]string{{"S", "M1"}, {"S", "M2"}, {"M1", "T"}, {"M2", "T"}})
}

func chain(k int) *Pipeline {
	p := New()
	_ = p.Add("n000", 1)
	for i := 1; i <= k+1; i++ {
		n := fmt.Sprintf("n%03d", i)
		_ = p.Add(n, 1)
		_ = p.Link(fmt.Sprintf("n%03d", i-1), n)
	}
	return p
}
