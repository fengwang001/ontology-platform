package report

import (
	"math/rand"
	"slices"
	"testing"
)

// build records the same facts in a seed-dependent order.
func build(seed int64) string {
	r := New()
	r.RejectedQuery = true
	cols := []string{"secret", "salary", "ssn", "token"}
	for _, i := range rand.New(rand.NewSource(seed)).Perm(len(cols)) {
		r.Drop(cols[i])
	}
	type ref struct{ col, path string }
	refs := []ref{{"secret", "$/or[0]"}, {"secret", "$/or[1]/not"}, {"salary", "$/and[0]"}}
	for _, i := range rand.New(rand.NewSource(seed + 100)).Perm(len(refs)) {
		r.Reject(refs[i].col, refs[i].path)
	}
	return r.Canonical()
}

// TestCanonicalDeterministic shuffles construction order twenty times and
// requires byte-identical canonical output.
func TestCanonicalDeterministic(t *testing.T) {
	base := build(0)
	for seed := int64(1); seed < 20; seed++ {
		if got := build(seed); got != base {
			t.Fatalf("seed %d: canonical output differs:\n%s\nvs\n%s", seed, got, base)
		}
	}
}

// TestContents checks sorting, per-column path merging, and exact bytes.
func TestContents(t *testing.T) {
	r := New()
	r.RejectedQuery = true
	r.Drop("secret", "salary", "secret")
	r.Reject("secret", "$/or[1]/not", "$/or[0]")
	r.Reject("salary", "$/and[0]")
	if got := r.Dropped(); !slices.Equal(got, []string{"salary", "secret"}) {
		t.Errorf("Dropped() = %v, want [salary secret]", got)
	}
	refs := r.Refs()
	if len(refs) != 2 || refs[0].Col != "salary" || refs[1].Col != "secret" {
		t.Fatalf("Refs() = %+v, want salary then secret", refs)
	}
	if !slices.Equal(refs[1].Paths, []string{"$/or[0]", "$/or[1]/not"}) {
		t.Errorf("secret to report one column with all its paths, got %v", refs[1].Paths)
	}
	want := "rejected: yes\ndropped: salary,secret\nrefs: salary@$/and[0],secret@$/or[0]|$/or[1]/not\n"
	if got := r.Canonical(); got != want {
		t.Errorf("Canonical() = %q, want %q", got, want)
	}
	empty := New()
	if got := empty.Canonical(); got != "rejected: no\ndropped: \nrefs: \n" {
		t.Errorf("empty Canonical() = %q", got)
	}
}
