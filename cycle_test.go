package ontology

import (
	"reflect"
	"testing"
)

// 图：a->b->c->a 构成环，c->d->b 构成另一环，e->e 自环，a->e。
func buildCycleStore(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	declare(t, s, LinkType{Name: "depends", SourceType: "Node", TargetType: "Node",
		Cardinality: ManyToMany, Cascade: CascadeSetNull})
	addObjects(t, s, "Node", "a", "b", "c", "d", "e")
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}, {"d", "b"}, {"e", "e"}, {"a", "e"}} {
		link(t, s, "depends", key("Node", e[0]), key("Node", e[1]))
	}
	return s
}

func cycleSources(cyc []PathStep) []string {
	out := make([]string, len(cyc))
	for i, st := range cyc {
		out[i] = st.Source.ID
	}
	return out
}

func TestFindCycles(t *testing.T) {
	s := buildCycleStore(t)
	cycles := s.FindCycles(key("Node", "a"), []string{"depends"})
	if len(cycles) != 3 {
		t.Fatalf("want 3 cycles, got %v", cycles)
	}
	var got [][]string
	for _, c := range cycles {
		got = append(got, cycleSources(c))
	}
	want := [][]string{{"a", "b", "c"}, {"b", "c", "d"}, {"e"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// 每个环末步的 Target 必须回到首步的 Source。
	for _, c := range cycles {
		if c[len(c)-1].Target != c[0].Source {
			t.Fatalf("cycle not closed: %v", c)
		}
	}
}

func TestFindCyclesDeterministic(t *testing.T) {
	s := buildCycleStore(t)
	first := s.FindCycles(key("Node", "a"), []string{"depends"})
	for i := 0; i < 20; i++ {
		if got := s.FindCycles(key("Node", "a"), []string{"depends"}); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs: %v vs %v", i, got, first)
		}
	}
	// 同一环从不同起点进入，规范化表示必须一致。
	fromB := s.FindCycles(key("Node", "b"), []string{"depends"})
	var ringA []PathStep
	for _, c := range fromB {
		if len(c) == 3 && c[0].Source.ID == "a" {
			ringA = c
		}
	}
	if ringA == nil {
		t.Fatalf("expected canonical a-b-c ring from b, got %v", fromB)
	}
}

func TestFindCyclesNoCycle(t *testing.T) {
	s := newStore(t)
	declare(t, s, LinkType{Name: "depends", SourceType: "Node", TargetType: "Node",
		Cardinality: ManyToMany, Cascade: CascadeSetNull})
	addObjects(t, s, "Node", "a", "b", "c")
	link(t, s, "depends", key("Node", "a"), key("Node", "b"))
	link(t, s, "depends", key("Node", "b"), key("Node", "c"))
	if got := s.FindCycles(key("Node", "a"), []string{"depends"}); len(got) != 0 {
		t.Fatalf("DAG must have no cycles, got %v", got)
	}
}
