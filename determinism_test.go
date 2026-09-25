package ontology

import (
	"math/rand"
	"strings"
	"testing"
)

type pair struct{ a, b string }

// unionWorkload returns the union pairs that build two classes:
// {alpha, bravo, charlie, delta} and {xenon, yttrium, zinc}.
func unionWorkload() ([]pair, []string) {
	pairs := []pair{
		{"alpha", "bravo"},
		{"bravo", "charlie"},
		{"charlie", "delta"},
		{"xenon", "yttrium"},
		{"yttrium", "zinc"},
	}
	ids := []string{"alpha", "bravo", "charlie", "delta", "xenon", "yttrium", "zinc"}
	return pairs, ids
}

func permutationsOf(pairs []pair) [][]pair {
	var out [][]pair
	var walk func(prefix []pair, rest []pair)
	walk = func(prefix, rest []pair) {
		if len(rest) == 0 {
			out = append(out, append([]pair(nil), prefix...))
			return
		}
		for i := range rest {
			next := append([]pair(nil), rest[:i]...)
			next = append(next, rest[i+1:]...)
			walk(append(prefix, rest[i]), next)
		}
	}
	walk(nil, pairs)
	return out
}

func findAll(t *testing.T, ds *DisjointSet, ids []string) []string {
	t.Helper()
	reps := make([]string, len(ids))
	for i, id := range ids {
		rep, err := ds.Find(id)
		if err != nil {
			t.Fatalf("Find(%q): %v", id, err)
		}
		reps[i] = rep
	}
	return reps
}

func TestFindIdenticalAcrossUnionOrders(t *testing.T) {
	pairs, ids := unionWorkload()
	perms := permutationsOf(pairs)
	if len(perms) < 20 {
		t.Fatalf("only %d permutations, want at least 20", len(perms))
	}

	var reference []string
	for i, order := range perms {
		var ds DisjointSet
		for _, p := range order {
			ds.Union(p.a, p.b)
		}
		reps := findAll(t, &ds, ids)
		if i == 0 {
			reference = reps
			continue
		}
		for j, id := range ids {
			if reps[j] != reference[j] {
				t.Fatalf("permutation %d: Find(%q) = %q, want %q (order-independent)",
					i, id, reps[j], reference[j])
			}
		}
	}
	// Sanity: the reference really is the lexicographic minimum per class.
	if reference[0] != "alpha" || reference[4] != "xenon" {
		t.Fatalf("unexpected representatives: %v", reference)
	}
}

func TestLateSmallerIDBecomesRepresentative(t *testing.T) {
	var ds DisjointSet
	// Build a large class first.
	members := []string{"mango", "melon", "milk", "mint", "mocha"}
	for i := 0; i+1 < len(members); i++ {
		ds.Union(members[i], members[i+1])
	}
	for _, id := range members {
		if rep, _ := ds.Find(id); rep != "mango" {
			t.Fatalf("Find(%q) = %q, want mango", id, rep)
		}
	}
	// Now merge in a lexicographically smaller ID at the very end.
	ds.Union("aardvark", "mocha")
	for _, id := range append(members, "aardvark") {
		if rep, _ := ds.Find(id); rep != "aardvark" {
			t.Fatalf("Find(%q) = %q after late merge, want aardvark", id, rep)
		}
	}
}

func serializeClasses(classes [][]string) string {
	var b strings.Builder
	for _, class := range classes {
		b.WriteString(strings.Join(class, ","))
		b.WriteString("\n")
	}
	return b.String()
}

func TestClassesByteIdenticalAcrossShuffles(t *testing.T) {
	pairs, _ := unionWorkload()
	// Add a singleton so Classes covers singleton classes too.
	extra := "solo"

	var reference string
	for seed := int64(0); seed < 30; seed++ {
		shuffled := append([]pair(nil), pairs...)
		rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		var ds DisjointSet
		for _, p := range shuffled {
			ds.Union(p.a, p.b)
		}
		ds.Add(extra)
		got := serializeClasses(ds.Classes())
		if seed == 0 {
			reference = got
			continue
		}
		if got != reference {
			t.Fatalf("seed %d: Classes output differs:\n%s\nwant:\n%s", seed, got, reference)
		}
	}

	want := "alpha,bravo,charlie,delta\nsolo\nxenon,yttrium,zinc\n"
	if reference != want {
		t.Fatalf("Classes output =\n%s\nwant:\n%s", reference, want)
	}
}

func TestClassesSortedByRepresentative(t *testing.T) {
	var ds DisjointSet
	// Deliberately union in an order that creates "unlucky" tree shapes.
	ds.Union("zz", "aa")
	ds.Union("mm", "bb")
	ds.Union("mm", "zz")
	classes := ds.Classes()
	if len(classes) != 1 {
		t.Fatalf("got %d classes, want 1", len(classes))
	}
	got := strings.Join(classes[0], ",")
	if got != "aa,bb,mm,zz" {
		t.Fatalf("class members = %s, want sorted aa,bb,mm,zz", got)
	}
	if rep, _ := ds.Find("zz"); rep != "aa" {
		t.Fatalf("Find(zz) = %q, want aa", rep)
	}
}
