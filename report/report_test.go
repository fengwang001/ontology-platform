package report_test

import (
	"testing"

	"ontology/report"
)

// Normalize sorts and dedupes both fields; String renders exact bytes.
func TestNormalizeAndString(t *testing.T) {
	r := report.Report{
		Rejected: true,
		Dropped:  []string{"secret", "city", "secret"},
		Refs: []report.Ref{
			{Column: "b", Path: "$.or[1]"},
			{Column: "a", Path: "$.or[0]"},
			{Column: "a", Path: "$.or[0]"},
		},
	}
	r.Normalize()
	want := "rejected: true\ndropped: city,secret\nrefs: a@$.or[0] b@$.or[1]\n"
	if got := r.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The same logical report built 20 times with shuffled field insertion
// order renders byte-identical output.
func TestDeterministicAcrossShuffles(t *testing.T) {
	build := func(seed int) string {
		dropped := []string{"secret", "salary", "city"}
		refs := []report.Ref{
			{Column: "secret", Path: "$.and[0]"},
			{Column: "salary", Path: "$.and[1].not"},
			{Column: "secret", Path: "$.and[2]"},
		}
		for i := range dropped {
			j := (seed + i*i + 1) % len(dropped)
			dropped[i], dropped[j] = dropped[j], dropped[i]
		}
		for i := range refs {
			j := (seed + i*3 + 2) % len(refs)
			refs[i], refs[j] = refs[j], refs[i]
		}
		r := report.Report{Rejected: true, Dropped: dropped, Refs: refs}
		r.Normalize()
		return r.String()
	}
	base := build(0)
	for seed := 1; seed < 20; seed++ {
		if got := build(seed); got != base {
			t.Fatalf("seed %d: got %q, want %q", seed, got, base)
		}
	}
}
