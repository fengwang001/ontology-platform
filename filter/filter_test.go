package filter

import (
	"errors"
	"reflect"
	"testing"

	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

func TestDenyProbes(t *testing.T) {
	accessPolicy := policy.New(map[string][]string{"r": {"id"}})
	cases := []struct {
		name string
		node *predicate.Node
		path string
	}{
		{"not eq", predicate.Not(predicate.Eq("secret", "1")), "$/not/eq"},
		{"is null", predicate.IsNull("secret"), "$/is-null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, audit, err := New(accessPolicy, "r").Authorize(tc.node)
			if !errors.Is(err, ErrDeniedColumn) {
				t.Fatalf("Authorize() error = %v", err)
			}
			if !audit.Denied || len(audit.References) != 1 ||
				audit.References[0].Column != "secret" || audit.References[0].Path != tc.path {
				t.Fatalf("audit = %#v", audit)
			}
		})
	}
}

func TestRowProjectionAndCounts(t *testing.T) {
	accessPolicy := policy.New(map[string][]string{"r": {"a", "empty"}})
	f := New(accessPolicy, "r")
	node := predicate.Not(predicate.Eq("a", "1"))
	rows, audit, err := f.Apply(node, []Row{{"a": "1", "empty": "", "secret": "x"}}, []string{"a", "empty", "secret"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{"a": "1", "empty": ""}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	if !reflect.DeepEqual(audit.DroppedColumns, []string{"secret"}) {
		t.Fatalf("dropped = %#v", audit.DroppedColumns)
	}
	if f.NodesVisited() != predicate.Count(node) || f.Copies() > 2 {
		t.Fatalf("nodes=%d copies=%d", f.NodesVisited(), f.Copies())
	}
}

func TestOrConstantElision(t *testing.T) {
	accessPolicy := policy.New(map[string][]string{"r": {}})
	cases := []struct {
		name string
		node *predicate.Node
		deny bool
	}{
		{"false branch removed", predicate.Or(predicate.Eq("secret", "1"), predicate.Const(false)), true},
		{"true branch dominates", predicate.Or(predicate.Eq("secret", "1"), predicate.Const(true)), false},
		{"only false constants", predicate.Or(predicate.Const(false), predicate.Const(false)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, audit, err := New(accessPolicy, "r").Authorize(tc.node)
			if errors.Is(err, ErrDeniedColumn) != tc.deny || audit.Denied != tc.deny {
				t.Fatalf("denied=%v err=%v audit=%#v", tc.deny, err, audit)
			}
		})
	}
}

func TestExhaustiveForms(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		columns := []string{}
		for i := 0; i < 3; i++ {
			if mask&(1<<i) != 0 {
				columns = append(columns, string(rune('a'+i)))
			}
		}
		accessPolicy := policy.New(map[string][]string{"r": columns})
		forms := []*predicate.Node{
			predicate.Eq("a", "x"),
			predicate.Not(predicate.Eq("a", "x")),
			predicate.And(predicate.Eq("a", "x"), predicate.Eq("a", "x")),
			predicate.Or(predicate.Eq("a", "x"), predicate.Eq("a", "x")),
		}
		for formIndex, form := range forms {
			_, _, err := New(accessPolicy, "r").Authorize(form)
			wantDeny := mask&1 == 0
			if errors.Is(err, ErrDeniedColumn) != wantDeny {
				t.Fatalf("mask=%d form=%d err=%v", mask, formIndex, err)
			}
		}
	}
}

func TestRepeatedColumnReferences(t *testing.T) {
	accessPolicy := policy.New(map[string][]string{"r": {}})
	node := predicate.Or(predicate.Eq("secret", "1"), predicate.Not(predicate.IsNull("secret")))
	_, audit, err := New(accessPolicy, "r").Authorize(node)
	if !errors.Is(err, ErrDeniedColumn) {
		t.Fatalf("err = %v", err)
	}
	want := []report.Reference{
		{Column: "secret", Path: "$/or[0]/eq"},
		{Column: "secret", Path: "$/or[1]/not/is-null"},
	}
	if !reflect.DeepEqual(audit.References, want) {
		t.Fatalf("references = %#v", audit.References)
	}
	if !reflect.DeepEqual(audit.DroppedColumns, []string(nil)) {
		t.Fatalf("dropped = %#v", audit.DroppedColumns)
	}
}
