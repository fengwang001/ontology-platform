package report

import (
	"math/rand"
	"testing"

	"ontology/predicate"
)

func TestDeterministicAcrossShuffles(t *testing.T) {
	pruned := []string{"secret", "salary", "ssn"}
	refs := []predicate.Ref{
		{Column: "secret", Path: "$/AND[0]"},
		{Column: "secret", Path: "$/AND[1]/OR[0]"},
		{Column: "salary", Path: "$/AND[1]/OR[1]"},
	}
	want := New(pruned, refs, true).String()
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		pc := append([]string(nil), pruned...)
		rc := append([]predicate.Ref(nil), refs...)
		rng.Shuffle(len(pc), func(i, j int) { pc[i], pc[j] = pc[j], pc[i] })
		rng.Shuffle(len(rc), func(i, j int) { rc[i], rc[j] = rc[j], rc[i] })
		if got := New(pc, rc, true).String(); got != want {
			t.Fatalf("seed %d: report differs:\n%s\nwant:\n%s", seed, got, want)
		}
	}
}

func TestRendering(t *testing.T) {
	r := New(
		[]string{"b", "a", "b"},
		[]predicate.Ref{
			{Column: "s", Path: "$/AND[1]"},
			{Column: "s", Path: "$/AND[0]"},
			{Column: "s", Path: "$/AND[0]"},
		},
		true,
	)
	want := "rejected: true\n" +
		"pruned-columns: a,b\n" +
		"rejected-refs: s@($/AND[0],$/AND[1])\n"
	if got := r.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len(r.RejectedRefs) != 1 || len(r.RejectedRefs[0].Paths) != 2 {
		t.Fatalf("same column must be reported once with all paths: %+v", r.RejectedRefs)
	}
}

func TestEmptyReport(t *testing.T) {
	want := "rejected: false\npruned-columns: \nrejected-refs:\n"
	if got := New(nil, nil, false).String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
