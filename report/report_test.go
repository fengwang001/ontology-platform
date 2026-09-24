package report

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"ontology/filter"
	"ontology/policy"
)

func TestReportDeterminism(t *testing.T) {
	pol := policy.New("id", "region", "secret", "other")
	if err := pol.Grant("viewer", "id", "region"); err != nil {
		t.Fatal(err)
	}
	refs := []filter.Ref{
		{Column: "secret", Path: []int{1, 0}},
		{Column: "other", Path: []int{0}},
		{Column: "secret", Path: []int{0, 1}},
	}
	baseline := Build(pol, "viewer", refs, true, 6, 2).String()
	for iter := 0; iter < 20; iter++ {
		shuffled := append([]filter.Ref(nil), refs...)
		rand.New(rand.NewPCG(uint64(iter), 99)).Shuffle(len(shuffled),
			func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		pol2 := policy.New("other", "secret", "region", "id")
		if err := pol2.Grant("viewer", "region", "id"); err != nil {
			t.Fatal(err)
		}
		got := Build(pol2, "viewer", shuffled, true, 6, 2).String()
		if got != baseline {
			t.Fatalf("iter %d:\n%s\n!=\n%s", iter, got, baseline)
		}
	}
}

func TestReportContent(t *testing.T) {
	cases := []struct {
		name    string
		granted []string
		refs    []filter.Ref
		removed []string
		columns []string
	}{
		{"rejected", []string{"id", "region"},
			[]filter.Ref{{Column: "secret", Path: []int{0}}},
			[]string{"other", "secret"}, []string{"secret"}},
		{"allowed full", []string{"id", "region", "secret", "other"}, nil,
			nil, nil},
		{"empty grant", nil, nil,
			[]string{"id", "other", "region", "secret"}, nil},
		{"same column twice", []string{"id", "region", "other"},
			[]filter.Ref{
				{Column: "secret", Path: []int{1}},
				{Column: "secret", Path: []int{0}},
			},
			[]string{"secret"}, []string{"secret"}},
	}
	for _, tc := range cases {
		pol := policy.New("id", "region", "secret", "other")
		if err := pol.Grant("viewer", tc.granted...); err != nil {
			t.Fatal(err)
		}
		r := Build(pol, "viewer", tc.refs, len(tc.refs) > 0, 3, 2)
		if !reflect.DeepEqual(r.RemovedColumns, tc.removed) {
			t.Errorf("%s: removed=%v want %v", tc.name, r.RemovedColumns, tc.removed)
		}
		if !reflect.DeepEqual(r.RejectedColumns(), tc.columns) {
			t.Errorf("%s: columns=%v want %v", tc.name, r.RejectedColumns(), tc.columns)
		}
		if r.QueryRejected != (len(tc.refs) > 0) {
			t.Errorf("%s: rejected flag wrong", tc.name)
		}
		if len(tc.refs) > 1 &&
			strings.Count(r.String(), "secret@") != len(tc.refs) {
			t.Errorf("%s: all paths must appear: %s", tc.name, r.String())
		}
	}
}

func TestReportStableAcrossPolicies(t *testing.T) {
	pol := policy.New("b", "a", "c")
	if err := pol.Grant("r", "a"); err != nil {
		t.Fatal(err)
	}
	r := Build(pol, "r", []filter.Ref{
		{Column: "c", Path: []int{0}},
		{Column: "b", Path: []int{1}},
	}, true, 2, 1)
	want := "role=r rejected=true removed=[b,c]\nnodes=2 copies=1\nreferences=b@[1];c@[0]\n"
	if r.String() != want {
		t.Fatalf("report:\n%s", r.String())
	}
}
