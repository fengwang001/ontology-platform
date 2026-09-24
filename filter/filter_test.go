package filter

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"ontology/policy"
	"ontology/predicate"
)

func testPolicy() *policy.Policy {
	p := policy.New()
	p.Grant("analyst", "dept", "name")
	p.Grant("empty")
	p.Grant("admin", "dept", "name", "secret")
	return p
}

func cmpC(col string) predicate.Compare { return predicate.Compare{Column: col, Value: "1"} }

func TestCheck(t *testing.T) {
	pol := testPolicy()
	cmpN := cmpC("name")
	kinds := []error{ErrUnknownRole, ErrInvisibleColumn, ErrInvalidPredicate}
	cases := []struct {
		name, role string
		pred       predicate.Pred
		want       error
	}{
		{"nil predicate passes", "analyst", nil, nil},
		{"const only passes", "analyst", predicate.Const{Value: true}, nil},
		{"visible compare passes", "analyst", cmpN, nil},
		{"invisible compare rejected", "analyst", cmpC("secret"), ErrInvisibleColumn},
		{"invisible is null rejected", "analyst", predicate.IsNull{Column: "secret"}, ErrInvisibleColumn},
		{"or true absorbs invisible", "analyst", predicate.Or{L: predicate.Const{Value: true}, R: cmpC("secret")}, nil},
		{"and false absorbs invisible", "analyst", predicate.And{L: cmpC("secret"), R: predicate.Const{Value: false}}, nil},
		{"or false keeps invisible", "analyst", predicate.Or{L: predicate.Const{Value: false}, R: cmpC("secret")}, ErrInvisibleColumn},
		{"and true keeps invisible", "analyst", predicate.And{L: predicate.Const{Value: true}, R: cmpC("secret")}, ErrInvisibleColumn},
		{"unknown role", "ghost", cmpN, ErrUnknownRole},
		{"empty set rejects any column", "empty", cmpN, ErrInvisibleColumn},
		{"empty set allows nil pred", "empty", nil, nil},
		{"full set allows secret", "admin", cmpC("secret"), nil},
		{"nil child invalid", "analyst", predicate.Not{}, ErrInvalidPredicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := new(Checker).Check(tc.pred, pol, tc.role)
			for _, k := range kinds {
				if got := errors.Is(err, k); got != (k == tc.want) {
					t.Errorf("errors.Is(err, %v) = %v, want %v (err=%v)", k, got, k == tc.want, err)
				}
			}
		})
	}
}

// TestCounterexampleProbes: probes under which 甲/乙 return rows must be rejected.
func TestCounterexampleProbes(t *testing.T) {
	probes := []struct {
		name, path string
		pred       predicate.Pred
	}{
		{"NOT (secret = 1)", "$.N", predicate.Not{Inner: cmpC("secret")}},
		{"secret IS NULL", "$", predicate.IsNull{Column: "secret"}},
	}
	for _, pr := range probes {
		t.Run(pr.name, func(t *testing.T) {
			err := new(Checker).Check(pr.pred, testPolicy(), "analyst")
			var rej *RejectError
			if !errors.As(err, &rej) || !errors.Is(err, ErrInvisibleColumn) {
				t.Fatalf("probe must be rejected with ErrInvisibleColumn, got %v", err)
			}
			if len(rej.Refs) != 1 || rej.Refs[0].Column != "secret" ||
				len(rej.Refs[0].Paths) != 1 || rej.Refs[0].Paths[0] != pr.path {
				t.Fatalf("want secret@%s, got %v", pr.path, err)
			}
		})
	}
}

func TestTraversal(t *testing.T) {
	pred := predicate.And{
		L: predicate.Or{L: predicate.Compare{Column: "name", Value: "a"}, R: predicate.Compare{Column: "dept", Value: "e"}},
		R: predicate.Not{Inner: predicate.Compare{Column: "name", Value: "b"}},
	}
	var c Checker
	if err := c.Check(pred, testPolicy(), "analyst"); err != nil {
		t.Fatal(err)
	}
	if want := predicate.NodeCount(predicate.Fold(pred)); c.Visited() != want {
		t.Errorf("visited %d nodes, want exactly %d (single pass)", c.Visited(), want)
	}
	dup := predicate.Or{L: cmpC("secret"), R: predicate.And{L: cmpC("secret"), R: cmpC("secret")}}
	err := new(Checker).Check(dup, testPolicy(), "analyst")
	var rej *RejectError
	if !errors.As(err, &rej) || len(rej.Refs) != 1 {
		t.Fatalf("want one ref for secret, got %v", err)
	}
	if got := strings.Join(rej.Refs[0].Paths, ","); got != "$.L,$.R.L,$.R.R" {
		t.Errorf("paths = %v, want $.L,$.R.L,$.R.R", rej.Refs[0].Paths)
	}
}

func TestPruneRow(t *testing.T) {
	row := map[string]string{"name": "ada", "dept": "eng", "secret": "x"}
	pruned, err := new(Pruner).PruneRow(row, testPolicy(), "analyst")
	if err != nil {
		t.Fatal(err)
	}
	pairs := make([]string, 0, len(pruned))
	for k, v := range pruned {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	if got := strings.Join(pairs, ","); got != "dept=eng,name=ada" {
		t.Errorf("pruned row = %q, want %q (sorted-key byte compare)", got, "dept=eng,name=ada")
	}
	if _, hidden := pruned["secret"]; hidden {
		t.Error("invisible column must be removed, not zeroed: distinguishable from zero-valued row")
	}
}

func TestPruneCostBound(t *testing.T) {
	wide := policy.New()
	wide.Grant("r", "c0", "c1", "c2", "c3", "c4")
	row := map[string]string{}
	for i := 0; i < 1000; i++ {
		row[fmt.Sprintf("c%d", i)] = "v"
	}
	var p Pruner
	if _, err := p.PruneRow(row, wide, "r"); err != nil {
		t.Fatal(err)
	}
	if p.Copies() > 4*5 {
		t.Errorf("copies = %d, want <= %d for 5 visible of 1000", p.Copies(), 4*5)
	}
}

// TestExhaustive: 8 masks x 4 shapes = 32 combos vs the documented rule.
func TestExhaustive(t *testing.T) {
	abc := []string{"a", "b", "c"}
	forms := []struct {
		name string
		pred predicate.Pred
		refs []string
	}{
		{"eq", cmpC("a"), []string{"a"}},
		{"not", predicate.Not{Inner: cmpC("a")}, []string{"a"}},
		{"and", predicate.And{L: predicate.And{L: cmpC("a"), R: cmpC("b")}, R: cmpC("c")}, abc},
		{"or", predicate.Or{L: predicate.Or{L: cmpC("a"), R: cmpC("b")}, R: cmpC("c")}, abc},
	}
	allow, reject := 0, 0
	for mask := 0; mask < 8; mask++ {
		pol := policy.New()
		pol.Grant("r")
		vis := map[string]bool{}
		for i, c := range abc {
			if mask&(1<<i) != 0 {
				pol.Grant("r", c)
				vis[c] = true
			}
		}
		for _, f := range forms {
			wantNamed := []string{}
			for _, col := range f.refs {
				if !vis[col] {
					wantNamed = append(wantNamed, col)
				}
			}
			err := new(Checker).Check(f.pred, pol, "r")
			if got, want := err == nil, len(wantNamed) == 0; got != want {
				t.Errorf("mask=%03b form=%s: allowed=%v, want %v", mask, f.name, got, want)
			}
			if err == nil {
				allow++
				continue
			}
			reject++
			var rej *RejectError
			if errors.As(err, &rej) {
				named := []string{}
				for _, r := range rej.Refs {
					named = append(named, r.Column)
				}
				if strings.Join(named, ",") != strings.Join(wantNamed, ",") {
					t.Errorf("mask=%03b form=%s: named %v, want %v", mask, f.name, named, wantNamed)
				}
			} else {
				t.Errorf("mask=%03b form=%s: want RejectError, got %v", mask, f.name, err)
			}
		}
	}
	if allow+reject != 32 {
		t.Fatalf("covered %d combos, want 32", allow+reject)
	}
	t.Logf("exhaustive: %d allowed, %d rejected", allow, reject)
}
