package ontology

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

func ids(es []Element) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func pushAll(sel *TopK, es []Element) {
	for _, e := range es {
		sel.Push(e.ID, e.Score)
	}
}

// Desc 下取最大的 K 个，并列一律按 ID 升序，不随方向翻转。
func TestDescTiesBrokenByIDAscending(t *testing.T) {
	sel, err := New(3, Desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushAll(sel, []Element{
		{"a", 1}, {"b", 3}, {"c", 2}, {"d", 3}, {"e", 3},
	})
	got := ids(sel.Snapshot())
	want := []string{"b", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desc ties: got %v want %v", got, want)
	}
}

// Asc 下取最小的 K 个，并列同样按 ID 升序。
func TestAscTiesBrokenByIDAscending(t *testing.T) {
	sel, _ := New(3, Asc)
	pushAll(sel, []Element{
		{"a", 1}, {"b", 3}, {"c", 1}, {"d", 1},
	})
	got := ids(sel.Snapshot())
	want := []string{"a", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("asc ties: got %v want %v", got, want)
	}
}

// 并列恰好跨在第 K 与第 K+1 名上：留下 ID 字典序更小者。
func TestTieAtCutoffKeepsSmallerID(t *testing.T) {
	for _, dir := range []Direction{Desc, Asc} {
		sel, _ := New(2, dir)
		// 三个同分元素恰好填满 K=2 加一个边界外元素。
		pushAll(sel, []Element{{"zeta", 5}, {"alpha", 5}, {"mid", 5}})
		got := ids(sel.Snapshot())
		want := []string{"alpha", "mid"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dir=%v cutoff tie: got %v want %v", dir, got, want)
		}
	}
}

// 同一批元素以任意顺序 Push，Snapshot 必须逐元素一致。
func TestOrderIndependentOfArrival(t *testing.T) {
	input := []Element{
		{"id-01", 7}, {"id-02", 2}, {"id-03", 7}, {"id-04", 9},
		{"id-05", 7}, {"id-06", 1}, {"id-07", 9}, {"id-08", 2},
	}
	rng := rand.New(rand.NewPCG(42, 99))
	reference := runShuffled(t, Desc, 3, input, nil)
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(input))
		shuffled := make([]Element, len(input))
		for i, p := range perm {
			shuffled[i] = input[p]
		}
		got := runShuffled(t, Desc, 3, shuffled, nil)
		if !reflect.DeepEqual(got, reference) {
			t.Fatalf("trial %d: got %v want %v", trial, got, reference)
		}
	}
}

func runShuffled(t *testing.T, dir Direction, k int, es []Element, _ any) []Element {
	t.Helper()
	sel, err := New(k, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushAll(sel, es)
	return sel.Snapshot()
}
