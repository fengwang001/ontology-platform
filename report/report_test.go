package report

import (
	"math/rand"
	"strings"
	"testing"

	"ontology/filter"
)

func TestStringFormat(t *testing.T) {
	cases := []struct {
		name string
		rep  Report
		want string
	}{
		{"empty report", New(false, nil, nil), "rejected=false\npruned=\n"},
		{
			"pruned sorted",
			New(false, []string{"ssn", "secret"}, nil),
			"rejected=false\npruned=secret,ssn\n",
		},
		{
			"rejected with refs",
			New(true, []string{"secret"}, []filter.Ref{
				{Column: "secret", Paths: []string{"$.R", "$.L"}},
			}),
			"rejected=true\npruned=secret\nref=secret@$.L,$.R\n",
		},
		{
			"refs merged by column",
			New(true, nil, []filter.Ref{
				{Column: "b", Paths: []string{"$.R"}},
				{Column: "a", Paths: []string{"$.L"}},
				{Column: "b", Paths: []string{"$.L"}},
			}),
			"rejected=true\npruned=\nref=a@$.L\nref=b@$.L,$.R\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rep.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeterministic(t *testing.T) {
	pruned := []string{"secret", "ssn", "salary"}
	refs := []filter.Ref{
		{Column: "secret", Paths: []string{"$.L", "$.R.L"}},
		{Column: "ssn", Paths: []string{"$.R.R"}},
	}
	base := New(true, pruned, refs).String()
	for i := 0; i < 20; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		p := append([]string(nil), pruned...)
		rng.Shuffle(len(p), func(a, b int) { p[a], p[b] = p[b], p[a] })
		r := []filter.Ref{}
		for _, ref := range refs {
			paths := append([]string(nil), ref.Paths...)
			rng.Shuffle(len(paths), func(a, b int) { paths[a], paths[b] = paths[b], paths[a] })
			r = append(r, filter.Ref{Column: ref.Column, Paths: paths})
		}
		rng.Shuffle(len(r), func(a, b int) { r[a], r[b] = r[b], r[a] })
		if got := New(true, p, r).String(); got != base {
			t.Fatalf("shuffle %d changed output:\n%s\nwant:\n%s", i, got, base)
		}
	}
	if !strings.Contains(base, "rejected=true") || !strings.Contains(base, "pruned=salary,secret,ssn") {
		t.Fatalf("unexpected base report:\n%s", base)
	}
}
