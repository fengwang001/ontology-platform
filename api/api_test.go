package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/dag"
)

// EndToEnd must equal the naive enumeration over all source→sink paths.
func TestEndToEndMatchesBrute(t *testing.T) {
	graphs := map[string]*Pipeline{
		"five-node": fiveNode(),
		"tie":       tieGraph(),
		"chain-50":  chain(48),
		"wide": build([]op{{"S", 2}, {"a", 3}, {"b", 7}, {"c", 1}, {"T", 4}},
			[][2]string{{"S", "a"}, {"S", "b"}, {"S", "c"}, {"a", "T"}, {"b", "T"}, {"c", "T"}}),
	}
	for name, q := range graphs {
		got, err := q.EndToEnd()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want, _ := brute(q.g); got != want {
			t.Errorf("%s: EndToEnd=%d, brute=%d", name, got, want)
		}
	}
}

// The bottleneck sits on the critical path; ties break to the smallest name,
// and the global max-latency operator off the path must not be picked.
func TestBottleneckTie(t *testing.T) {
	cases := []struct {
		name     string
		p        *Pipeline
		wantName string
		wantLat  int64
	}{
		{"global max off path", fiveNode(), "A", 30}, // B(50) is off the critical path
		{"branch tie", tieGraph(), "M1", 10},
		{"all equal", chain(5), "n000", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, lat, err := c.p.Bottleneck()
			if err != nil || name != c.wantName || lat != c.wantLat {
				t.Errorf("Bottleneck=(%s,%d,%v), want (%s,%d,nil)", name, lat, err, c.wantName, c.wantLat)
			}
		})
	}
}

// Every rejected operation maps to its own sentinel and leaves state
// (names, end-to-end latency) untouched; the pipeline stays usable.
func TestRejectedOpsLeaveState(t *testing.T) {
	newP := func() *Pipeline {
		return build([]op{{"S", 0}, {"A", 10}, {"T", 0}},
			[][2]string{{"S", "A"}, {"A", "T"}})
	}
	bads := []struct {
		name string
		op   func(p *Pipeline) error
		want error
	}{
		{"duplicate Add", func(p *Pipeline) error { return p.Add("A", 1) }, dag.ErrDuplicate},
		{"negative latency", func(p *Pipeline) error { return p.Add("X", -1) }, dag.ErrNegative},
		{"unknown from", func(p *Pipeline) error { return p.Link("ghost", "T") }, dag.ErrUnknown},
		{"unknown to", func(p *Pipeline) error { return p.Link("S", "ghost") }, dag.ErrUnknown},
		{"cycle", func(p *Pipeline) error { return p.Link("T", "S") }, dag.ErrCycle},
		{"self-loop", func(p *Pipeline) error { return p.Link("A", "A") }, dag.ErrCycle},
	}
	for _, b := range bads {
		t.Run(b.name, func(t *testing.T) {
			p := newP()
			before, _ := p.EndToEnd()
			names := fmt.Sprint(p.g.Names())
			if err := b.op(p); !errors.Is(err, b.want) {
				t.Fatalf("got %v, want %v", err, b.want)
			}
			after, _ := p.EndToEnd()
			if before != after || names != fmt.Sprint(p.g.Names()) {
				t.Fatal("rejected op changed state")
			}
			if err := p.Add("still-usable", 1); err != nil {
				t.Fatalf("pipeline unusable after rejection: %v", err)
			}
		})
	}
	kinds := []error{dag.ErrDuplicate, dag.ErrNegative, dag.ErrUnknown, dag.ErrCycle, dag.ErrEmptyName}
	for i := range kinds {
		for j := range kinds {
			if i != j && errors.Is(kinds[i], kinds[j]) {
				t.Errorf("sentinels %v and %v not distinct", kinds[i], kinds[j])
			}
		}
	}
}

// EndToEnd must report ErrEndpoints when sources/sinks are not exactly one.
func TestEndpointsError(t *testing.T) {
	cases := map[string]*Pipeline{
		"two sources": build([]op{{"a", 1}, {"b", 1}, {"t", 1}}, [][2]string{{"a", "t"}, {"b", "t"}}),
		"two sinks":   build([]op{{"s", 1}, {"a", 1}, {"b", 1}}, [][2]string{{"s", "a"}, {"s", "b"}}),
		"empty":       New(),
	}
	for name, p := range cases {
		if _, err := p.EndToEnd(); !errors.Is(err, dag.ErrEndpoints) {
			t.Errorf("%s: got %v, want ErrEndpoints", name, err)
		}
	}
}

// Concurrent read-only queries must all return identical results.
func TestConcurrentReads(t *testing.T) {
	p := fiveNode()
	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	good := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for k := 0; k < 20; k++ {
				total, err := p.EndToEnd()
				name, lat, _ := p.Bottleneck()
				good[i] = err == nil && total == 60 && name == "A" && lat == 30 && p.SelfCheck() == nil
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, ok := range good {
		if !ok {
			t.Errorf("goroutine %d: got differing results", i)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
