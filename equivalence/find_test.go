package equivalence_test

import (
	"math/rand"
	"testing"

	"ontology/equivalence"
)

func samplePairs() [][2]string {
	return [][2]string{
		{"g", "k"}, {"a", "d"}, {"d", "g"}, {"k", "z"},
		{"b", "h"}, {"h", "m"}, {"c", "z"}, {"e", "q"},
		{"q", "b"}, {"f", "t"}, {"t", "a"}, {"i", "l"},
		{"l", "s"}, {"s", "e"}, {"j", "p"}, {"p", "f"},
		{"n", "w"}, {"w", "i"}, {"o", "y"}, {"r", "x"},
	}
}

func allSampleIDs() []string {
	return []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
		"w", "x", "y", "z",
	}
}

func findAll(t *testing.T, uf *equivalence.UnionFind, ids []string) []string {
	t.Helper()
	out := make([]string, len(ids))
	for i, id := range ids {
		rep, err := uf.Find(id)
		if err != nil {
			t.Fatalf("Find(%q): %v", id, err)
		}
		out[i] = rep
	}
	return out
}

// 同一批 Union 以至少 20 种不同顺序执行，每个元素的 Find 结果必须完全一致。
func TestFindIndependentOfOrder(t *testing.T) {
	pairs := samplePairs()
	ids := allSampleIDs()

	var baseline []string
	for seed := int64(0); seed < 20; seed++ {
		uf := equivalence.New()
		order := rand.New(rand.NewSource(seed)).Perm(len(pairs))
		for _, idx := range order {
			p := pairs[idx]
			if _, _, err := uf.Union(p[0], p[1]); err != nil {
				t.Fatalf("Union: %v", err)
			}
		}
		got := findAll(t, uf, ids)
		if baseline == nil {
			baseline = got
			continue
		}
		for i := range got {
			if got[i] != baseline[i] {
				t.Fatalf("seed %d: Find(%q)=%q, 基线为 %q", seed, ids[i], got[i], baseline[i])
			}
		}
	}

	// 用独立参考实现计算期望代表元（每分量字典序最小）。
	want := referenceRepresentatives(pairs)
	for i, id := range ids {
		if baseline[i] != want[id] {
			t.Fatalf("Find(%q)=%q，期望 %q", id, baseline[i], want[id])
		}
	}
}

func referenceRepresentatives(pairs [][2]string) map[string]string {
	groups := map[string]map[string]struct{}{}
	repOf := map[string]string{}
	add := func(id string) {
		if _, ok := groups[id]; !ok {
			groups[id] = map[string]struct{}{id: {}}
		}
	}
	for _, p := range pairs {
		add(p[0])
		add(p[1])
		g1, g2 := groups[p[0]], groups[p[1]]
		if len(g1) == 0 || len(g2) == 0 || sameSet(g1, g2) {
			continue
		}
		for id := range g2 {
			g1[id] = struct{}{}
			groups[id] = g1
		}
	}
	for id, g := range groups {
		min := id
		for member := range g {
			if member < min {
				min = member
			}
		}
		repOf[id] = min
	}
	return repOf
}

func sameSet(g1, g2 map[string]struct{}) bool {
	for id := range g1 {
		if _, ok := g2[id]; ok {
			return true
		}
	}
	return false
}

// 大类建好后再并入一个字典序更小的新元素，代表元必须更新为新元素。
func TestRepresentativeUpdatesWhenSmallerIDJoinsLate(t *testing.T) {
	uf := equivalence.New()
	members := []string{"b", "c", "d", "e", "f"}
	for i := 0; i+1 < len(members); i++ {
		if _, _, err := uf.Union(members[i], members[i+1]); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := uf.Find("f")
	if err != nil {
		t.Fatal(err)
	}
	if rep != "b" {
		t.Fatalf("并入更小 ID 前代表元应为 b，得到 %q", rep)
	}

	if _, _, err := uf.Union("f", "a"); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(members, "a") {
		rep, err := uf.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		if rep != "a" {
			t.Fatalf("并入 a 后 Find(%q)=%q，期望 a", id, rep)
		}
	}
}
