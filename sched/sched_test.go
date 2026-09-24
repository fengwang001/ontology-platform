package sched

import (
	"strconv"
	"testing"

	"ontology/graph"
)

func buildGraph(n int, edges [][2]int, ids func(int) string) *graph.Graph {
	g := graph.New()
	for i := 0; i < n; i++ {
		g.Add(ids(i))
	}
	for _, e := range edges {
		if err := g.AddEdge(ids(e[0]), ids(e[1])); err != nil {
			panic(err)
		}
	}
	return g
}

// simulate 用调度器跑完整张图（全部成功），返回启动顺序与内部计数器。
func simulate(g *graph.Graph, limit int) (order []string, peak, decisions int) {
	s := New(g, limit)
	running := []string{}
	for len(order) < len(g.Nodes()) {
		for {
			id := s.Next()
			if id == "" {
				break
			}
			running = append(running, id)
		}
		if len(running) == 0 {
			break
		}
		done := running[0]
		running = running[1:]
		s.Done(done)
		order = append(order, done)
	}
	return order, s.peak, s.decisions
}

func TestScheduler(t *testing.T) {
	padID := func(i int) string { return "t" + strconv.Itoa((i/100)%10) + strconv.Itoa((i/10)%10) + strconv.Itoa(i%10) }
	chainEdges := make([][2]int, 0, 999)
	for i := 1; i < 1000; i++ {
		chainEdges = append(chainEdges, [2]int{i - 1, i})
	}
	cases := []struct {
		name          string
		n             int
		edges         [][2]int
		limit         int
		wantPeak      int
		wantDecisions int // 上界 4*(n+e)
		prefix        []string
	}{
		{"500-independent-limit8", 500, nil, 8, 8, 4 * 500, []string{"t000", "t001"}},
		{"chain1000", 1000, chainEdges, 3, 1, 4 * (1000 + 999), []string{"t000"}},
		{"diamond-limit4", 4, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}}, 4, 2, 4 * (4 + 4),
			[]string{"t000", "t001", "t002", "t003"}},
		{"serial-limit1", 10, nil, 1, 1, 4 * 10, []string{"t000", "t001", "t002"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(tc.n, tc.edges, padID)
			order, peak, decisions := simulate(g, tc.limit)
			if len(order) != tc.n {
				t.Fatalf("ran %d of %d", len(order), tc.n)
			}
			if peak != tc.wantPeak {
				t.Fatalf("peak=%d want %d", peak, tc.wantPeak)
			}
			if decisions > tc.wantDecisions {
				t.Fatalf("decisions=%d > bound %d", decisions, tc.wantDecisions)
			}
			for i, want := range tc.prefix {
				if order[i] != want {
					t.Fatalf("order[%d]=%q want %q (full=%v)", i, order[i], want, order[:min(len(order), 8)])
				}
			}
		})
	}
}

func TestDiscardReleasesSlot(t *testing.T) {
	cases := []struct {
		name       string
		wasRunning bool
		wantID     string
	}{
		{"running-freed", true, "b"},
		{"unstarted-no-slot", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := graph.New()
			g.Add("a")
			g.Add("b")
			s := New(g, 1)
			first := s.Next()
			if first != "a" {
				t.Fatalf("first=%q want a", first)
			}
			if got := s.Next(); got != "" {
				t.Fatalf("expected slot exhausted, got %q", got)
			}
			s.Discard("a", tc.wasRunning)
			if got := s.Next(); got != tc.wantID {
				t.Fatalf("after discard Next()=%q want %q", got, tc.wantID)
			}
		})
	}
}
