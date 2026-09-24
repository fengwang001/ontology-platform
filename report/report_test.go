package report

import (
	"strings"
	"testing"
)

func TestBuildDeterministic(t *testing.T) {
	dropped := []string{"secret", "salary", "secret", "age"}
	rejected := []Ref{
		{Column: "salary", Paths: [][]string{{"and[1]", "compare"}}},
		{Column: "secret", Paths: [][]string{{"or[0]", "compare"}}},
		{Column: "secret", Paths: [][]string{{"compare"}}},
	}
	elided := []Ref{
		{Column: "secret", Paths: [][]string{{"or[1]", "compare"}}},
	}

	want := Build(dropped, rejected, elided, true).Encode()

	tests := []struct {
		name string
		drop []string
		ref  []Ref
	}{
		{"shuffle a",
			[]string{"age", "secret", "salary", "secret"},
			[]Ref{
				{Column: "secret", Paths: [][]string{{"compare"}, {"or[0]", "compare"}}},
				{Column: "salary", Paths: [][]string{{"and[1]", "compare"}}},
			}},
		{"shuffle b",
			[]string{"salary", "secret", "age", "secret"},
			[]Ref{
				{Column: "salary", Paths: [][]string{{"and[1]", "compare"}}},
				{Column: "secret", Paths: [][]string{{"or[0]", "compare"}, {"compare"}}},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.drop, tt.ref, elided, true).Encode()
			if got != want {
				t.Fatalf("nondeterministic report:\n%s\nwant:\n%s", got, want)
			}
		})
	}

	r := Build(dropped, rejected, elided, true)
	if r.DroppedColumns[0] != "age" || len(r.DroppedColumns) != 3 {
		t.Fatalf("dropped = %v", r.DroppedColumns)
	}
	if r.Rejected[0].Column != "salary" || r.Rejected[1].Column != "secret" {
		t.Fatalf("rejected cols = %v", r.Rejected)
	}
	if len(r.Rejected[1].Paths) != 2 {
		t.Fatalf("same column paths must all be kept: %d", len(r.Rejected[1].Paths))
	}
	if !strings.Contains(want, "elided[0]=secret|or[1]>compare") {
		t.Fatalf("elided section missing: %s", want)
	}
}

func TestEmptyEncoding(t *testing.T) {
	got := Build(nil, nil, nil, false).Encode()
	want := "rejected_query=false\ndropped=\nrejected=-\nelided=-\n"
	if got != want {
		t.Fatalf("empty report:\n%q\nwant:\n%q", got, want)
	}
}
