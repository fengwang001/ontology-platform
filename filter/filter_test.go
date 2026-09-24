package filter

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"ontology/policy"
	"ontology/predicate"
)

func basePolicy(t *testing.T) *policy.Policy {
	t.Helper()
	pol := policy.New("id", "region", "secret")
	if err := pol.Grant("viewer", "id", "region"); err != nil {
		t.Fatal(err)
	}
	if err := pol.Grant("admin", "id", "region", "secret"); err != nil {
		t.Fatal(err)
	}
	return pol
}

func TestAnalyze(t *testing.T) {
	pol := basePolicy(t)
	cases := []struct {
		name   string
		role   string
		node   *predicate.Node
		wantOK bool
		wantEr error
	}{
		{"nil predicate allowed", "viewer", nil, true, nil},
		{"constant allowed", "viewer", predicate.NewConst(true), true, nil},
		{"visible comparison", "viewer", predicate.NewEq("id", "7"), true, nil},
		{"NOT secret probe rejected", "viewer",
			predicate.NewNot(predicate.NewEq("secret", "1")), false, ErrInvisibleColumn},
		{"IS NULL probe rejected", "viewer",
			predicate.NewIsNull("secret"), false, ErrInvisibleColumn},
		{"OR non-constant secret rejected", "viewer",
			predicate.NewOr(predicate.NewEq("id", "7"), predicate.NewEq("secret", "1")),
			false, ErrInvisibleColumn},
		{"OR constant-true absorbs secret", "viewer",
			predicate.NewOr(predicate.NewConst(true), predicate.NewEq("secret", "1")), true, nil},
		{"AND constant-false absorbs secret", "viewer",
			predicate.NewAnd(predicate.NewConst(false), predicate.NewEq("secret", "1")), true, nil},
		{"OR constant-true after NOT still absorbs", "viewer",
			predicate.NewOr(predicate.NewNot(predicate.NewConst(false)),
				predicate.NewEq("secret", "1")), true, nil},
		{"admin sees secret", "admin",
			predicate.NewNot(predicate.NewEq("secret", "1")), true, nil},
		{"unknown column", "viewer", predicate.NewEq("ghost", "1"),
			false, policy.ErrUnknownColumn},
		{"malformed", "viewer", predicate.NewAnd(predicate.NewEq("id", "7")),
			false, predicate.ErrInvalidPredicate},
	}
	for _, tc := range cases {
		a := NewAnalyzer(pol)
		_, _, err := a.Analyze(tc.role, tc.node)
		if tc.wantOK && err != nil {
			t.Errorf("%s: unexpected err %v", tc.name, err)
		}
		if !tc.wantOK && !errors.Is(err, tc.wantEr) {
			t.Errorf("%s: err=%v want %v", tc.name, err, tc.wantEr)
		}
	}
}

func TestRejectionRefs(t *testing.T) {
	pol := basePolicy(t)
	tree := predicate.NewOr(
		predicate.NewAnd(predicate.NewEq("id", "7"), predicate.NewEq("secret", "1")),
		predicate.NewNot(predicate.NewIsNull("secret")),
	)
	a := NewAnalyzer(pol)
	_, refs, err := a.Analyze("viewer", tree)
	if !errors.Is(err, ErrInvisibleColumn) {
		t.Fatalf("err=%v", err)
	}
	want := []Ref{
		{Column: "secret", Path: []int{0, 1}},
		{Column: "secret", Path: []int{1, 0}},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs=%v want %v", refs, want)
	}
	if a.NodesVisited() != predicate.Count(tree) {
		t.Fatalf("visited=%d nodes=%d", a.NodesVisited(), predicate.Count(tree))
	}

	absorbed := predicate.NewOr(predicate.NewConst(true), predicate.NewEq("secret", "1"))
	_, absorbedRefs, err := NewAnalyzer(pol).Analyze("viewer", absorbed)
	if err != nil || len(absorbedRefs) != 0 {
		t.Fatalf("absorbed branch: err=%v refs=%v", err, absorbedRefs)
	}
}

func TestSingleTraversal(t *testing.T) {
	pol := basePolicy(t)
	cases := []*predicate.Node{
		nil,
		predicate.NewEq("id", "7"),
		predicate.NewNot(predicate.NewIsNull("region")),
		predicate.NewAnd(
			predicate.NewOr(predicate.NewEq("id", "7"), predicate.NewConst(false)),
			predicate.NewNot(predicate.NewEq("region", "cn")),
			predicate.NewConst(true),
		),
	}
	for i, tree := range cases {
		a := NewAnalyzer(pol)
		_, _, _ = a.Analyze("viewer", tree)
		if got, want := a.NodesVisited(), predicate.Count(tree); got != want {
			t.Errorf("case %d: visited=%d want %d", i, got, want)
		}
	}
}

func TestProjectionCostAndShape(t *testing.T) {
	names := make([]string, 1000)
	row := Row{}
	for i := range names {
		names[i] = "c" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) +
			string(rune('a'+(i/676)%26))
		row[names[i]] = "v"
	}
	pol := policy.New(names...)
	if err := pol.Grant("r", names[0], names[1], names[2], names[3], names[4]); err != nil {
		t.Fatal(err)
	}
	a := NewAnalyzer(pol)
	out := a.ProjectRows("r", []Row{row})
	if len(out[0]) != 5 {
		t.Fatalf("projected %d columns", len(out[0]))
	}
	if a.Copies() > 4*5 {
		t.Fatalf("copies=%d exceeds bound %d", a.Copies(), 4*5)
	}
	keys := make([]string, 0, len(out[0]))
	for k := range out[0] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if keys[0] != names[0] || keys[4] != names[4] {
		t.Fatalf("projected keys wrong: %v", keys)
	}
}

func TestApplyAndProbes(t *testing.T) {
	pol := basePolicy(t)
	rows := []Row{{"id": "7", "region": "cn", "secret": "1"}}
	for _, tree := range []*predicate.Node{
		predicate.NewNot(predicate.NewEq("secret", "1")),
		predicate.NewIsNull("secret"),
	} {
		_, _, out, err := Apply(pol, "viewer", tree, rows)
		if !errors.Is(err, ErrInvisibleColumn) || out != nil {
			t.Fatalf("probe must reject: err=%v out=%v", err, out)
		}
	}
	folded, _, out, err := Apply(pol, "viewer",
		predicate.NewEq("id", "7"), rows)
	if err != nil || folded == nil {
		t.Fatalf("visible query: %v", err)
	}
	if !reflect.DeepEqual(out, []map[string]string{
		{"id": "7", "region": "cn"},
	}) {
		t.Fatalf("rows=%v", out)
	}
	if _, ok := out[0]["secret"]; ok {
		t.Fatal("invisible column must be removed, not zeroed")
	}
}
