package graph

import "testing"

func TestOutSortedDedup(t *testing.T) {
	cases := []struct {
		name  string
		edges [][2]string
		want  []string
	}{
		{"sorted", [][2]string{{"a", "c"}, {"a", "b"}}, []string{"b", "c"}},
		{"dup", [][2]string{{"a", "b"}, {"a", "b"}, {"a", "a"}}, []string{"a", "b"}},
		{"selfloop", [][2]string{{"x", "x"}}, []string{"x"}},
		{"none", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			b.Node("a")
			for _, e := range tc.edges {
				b.Edge(e[0], e[1])
			}
			got := b.Build().Out("a")
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %v want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestReachable(t *testing.T) {
	// 图：a->b,a->c,b->a(环),b->b(自环),d 孤立不可达。
	b := NewBuilder()
	b.Edge("a", "b")
	b.Edge("a", "c")
	b.Edge("b", "a")
	b.Edge("b", "b")
	b.Node("d")
	g := b.Build()
	cases := []struct {
		start string
		want  int
	}{
		{"a", 3}, {"b", 3}, {"c", 1}, {"d", 1}, {"missing", 0},
	}
	for _, tc := range cases {
		if got := g.Reachable(tc.start); got != tc.want {
			t.Errorf("Reachable(%q)=%d want %d", tc.start, got, tc.want)
		}
	}
}

func TestEmptyAndUnknown(t *testing.T) {
	g := NewBuilder().Build()
	if g.Has("x") || g.Reachable("x") != 0 || g.Out("x") != nil {
		t.Fatal("empty graph should report nothing")
	}
	if len(g.Nodes()) != 0 {
		t.Fatal("empty graph nodes")
	}
}

func TestWithout(t *testing.T) {
	b := NewBuilder()
	b.Edge("a", "b")
	b.Edge("a", "c")
	b.Edge("b", "c")
	g := b.Build()
	g2 := g.Without("c")
	if g2.Has("c") {
		t.Fatal("deleted node still present")
	}
	if len(g2.Out("a")) != 1 || g2.Out("a")[0] != "b" {
		t.Fatalf("incoming edge not removed: %v", g2.Out("a"))
	}
	if len(g2.Out("b")) != 0 {
		t.Fatalf("outgoing edge not removed: %v", g2.Out("b"))
	}
	// 原快照不变。
	if !g.Has("c") || len(g.Out("a")) != 2 {
		t.Fatal("Without mutated receiver")
	}
}
