package dag

import (
	"errors"
	"testing"
)

func TestDistDP(t *testing.T) {
	// 第三节五节点图：S(0) A(30) B(50) A2(30) T(0)
	g := New()
	for _, n := range []struct {
		name string
		lat  int64
	}{{"S", 0}, {"A", 30}, {"B", 50}, {"A2", 30}, {"T", 0}} {
		if err := g.Add(n.name, n.lat); err != nil {
			t.Fatalf("Add %s: %v", n.name, err)
		}
	}
	for _, e := range [][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}} {
		if err := g.Link(e[0], e[1]); err != nil {
			t.Fatalf("Link %v: %v", e, err)
		}
	}
	sol, err := g.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	wantDist := map[string]int64{"S": 0, "A": 30, "B": 50, "A2": 60, "T": 60}
	for n, w := range wantDist {
		if sol.Dist[n] != w {
			t.Errorf("dist[%s]=%d want %d", n, sol.Dist[n], w)
		}
	}
	wantPred := map[string]string{"A": "S", "B": "S", "A2": "A", "T": "A2"}
	for n, w := range wantPred {
		if sol.Pred[n] != w {
			t.Errorf("pred[%s]=%q want %q", n, sol.Pred[n], w)
		}
	}
	if sol.EndToEnd != 60 {
		t.Errorf("EndToEnd=%d want 60", sol.EndToEnd)
	}
}

func TestRelaxCountChain(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		g := New()
		names := make([]string, m)
		for i := 0; i < m; i++ {
			names[i] = string(rune('a'+i/26)) + string(rune('a'+i%26)) + "x"
			if err := g.Add(names[i], 1); err != nil {
				t.Fatalf("m=%d Add: %v", m, err)
			}
		}
		for i := 0; i+1 < m; i++ {
			if err := g.Link(names[i], names[i+1]); err != nil {
				t.Fatalf("m=%d Link: %v", m, err)
			}
		}
		if _, err := g.Solve(); err != nil {
			t.Fatalf("m=%d Solve: %v", m, err)
		}
		if got := g.relaxCount; got != m-1 {
			t.Errorf("m=%d relaxCount=%d want %d", m, got, m-1)
		}
	}
}

func TestValidationDistinctErrors(t *testing.T) {
	cases := []struct {
		name string
		run  func(g *Graph) error
		want error
	}{
		{"negative lat", func(g *Graph) error { return g.Add("x", -1) }, ErrNegativeLat},
		{"empty name", func(g *Graph) error { return g.Add("", 1) }, ErrEmptyName},
		{"duplicate", func(g *Graph) error {
			if err := g.Add("S", 0); err != nil {
				t.Fatal(err)
			}
			return g.Add("S", 1)
		}, ErrDuplicateNode},
		{"unknown from", func(g *Graph) error {
			if err := g.Add("S", 0); err != nil {
				t.Fatal(err)
			}
			return g.Link("X", "S")
		}, ErrUnknownNode},
		{"unknown to", func(g *Graph) error {
			if err := g.Add("S", 0); err != nil {
				t.Fatal(err)
			}
			return g.Link("S", "X")
		}, ErrUnknownNode},
		{"self loop", func(g *Graph) error {
			if err := g.Add("S", 0); err != nil {
				t.Fatal(err)
			}
			return g.Link("S", "S")
		}, ErrCycle},
		{"cycle", func(g *Graph) error {
			g.Add("a", 1)
			g.Add("b", 1)
			g.Link("a", "b")
			return g.Link("b", "a")
		}, ErrCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			err := tc.run(g)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}
