package filter

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"

	"ontology/policy"
	"ontology/predicate"
)

func testEngine() *Engine {
	return NewEngine(policy.New(map[string][]string{
		"analyst": {"name", "dept"},
		"admin":   {"name", "dept", "salary", "secret"},
		"empty":   {},
	}))
}

func assert(t *testing.T, name string, cond bool) {
	t.Helper()
	if !cond {
		t.Error(name)
	}
}

// TestCheckProbes: counterexamples against approaches 甲 and 乙.
func TestCheckProbes(t *testing.T) {
	probes := []struct {
		name, paths string
		pred        *predicate.Node
	}{
		{"NOT(secret=1)", "NOT/CMP", predicate.NotOf(predicate.Compare("secret", predicate.Eq, 1))},
		{"secret IS NULL", "CMP", predicate.Compare("secret", predicate.IsNull, nil)},
	}
	for _, tc := range probes {
		refs, err := testEngine().Check("analyst", tc.pred)
		named := len(refs) == 1 && refs[0].Col == "secret" && strings.Join(refs[0].Paths, ",") == tc.paths
		assert(t, tc.name+" rejected, named with path", errors.Is(err, ErrInvisibleColumn) && named)
	}
}

func TestErrorsDistinct(t *testing.T) {
	eng := testEngine()
	_, errInv := eng.Check("analyst", predicate.Compare("secret", predicate.Eq, 1))
	_, errRole := eng.Check("ghost", predicate.Compare("name", predicate.Eq, 1))
	_, errBad := eng.Check("analyst", &predicate.Node{Kind: predicate.Kind(99)})
	sentinels := []error{ErrInvisibleColumn, ErrUnknownRole, ErrInvalidPredicate}
	for i, err := range []error{errInv, errRole, errBad} {
		for j, s := range sentinels {
			assert(t, fmt.Sprintf("err %d vs sentinel %d", i, j), errors.Is(err, s) == (i == j))
		}
	}
}

func TestSingleTraversal(t *testing.T) {
	eq := predicate.Compare
	preds := []*predicate.Node{
		predicate.AndOf(eq("name", predicate.Eq, "x"),
			predicate.OrOf(eq("dept", predicate.Eq, "y"), predicate.NotOf(eq("dept", predicate.Eq, "z")))),
		predicate.OrOf(eq("name", predicate.Eq, "x"), predicate.True()),
		eq("name", predicate.Eq, "x"),
	}
	for i, p := range preds {
		eng := testEngine()
		_, err := eng.Check("analyst", p)
		want := predicate.NodeCount(predicate.Fold(p))
		assert(t, fmt.Sprintf("case %d one traversal", i), err == nil && eng.Visited() == want)
	}
}

func canonical(row map[string]any) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(row)) {
		fmt.Fprintf(&b, "%s=%v;", k, row[k])
	}
	return b.String()
}

func TestPruneRow(t *testing.T) {
	vis := []string{"c000", "c001", "c002", "c003", "c004"}
	eng := NewEngine(policy.New(map[string][]string{"r": vis}))
	row, rev := map[string]any{}, map[string]any{}
	for i := 0; i < 1000; i++ {
		row[fmt.Sprintf("c%03d", i)] = i
		rev[fmt.Sprintf("c%03d", 999-i)] = 999 - i
	}
	kept, pruned, err := eng.PruneRow("r", row)
	if err != nil {
		t.Fatal(err)
	}
	assert(t, "copies <= 4*visible", eng.Copies() <= 4*5)
	kept2, _, _ := eng.PruneRow("r", rev)
	assert(t, "pruned rows byte-identical regardless of insertion order", canonical(kept) == canonical(kept2))
	want := "c000=0;c001=1;c002=2;c003=3;c004=4;"
	assert(t, "kept row contains visible keys only, sorted", canonical(kept) == want)
	assert(t, "pruned names: 995 sorted", len(pruned) == 995 && sort.StringsAreSorted(pruned))
	_, present := kept["c005"]
	assert(t, "invisible column removed (not zero-valued)", !present)
}

func TestBoundaries(t *testing.T) {
	eng := testEngine()
	_, e1 := eng.Check("analyst", nil)
	_, e2 := eng.Check("analyst", predicate.AndOf(predicate.True(), predicate.NotOf(predicate.False())))
	_, e3 := eng.Check("empty", predicate.Compare("name", predicate.Eq, 1))
	kept, pruned, _ := eng.PruneRow("empty", map[string]any{"name": "a", "dept": "b"})
	_, e4 := eng.Check("admin", predicate.Compare("secret", predicate.Eq, 1))
	refs, e5 := eng.Check("analyst", predicate.OrOf(
		predicate.Compare("secret", predicate.Eq, 1), predicate.Compare("secret", predicate.Eq, 2)))
	assert(t, "nil predicate (no filter) allowed", e1 == nil)
	assert(t, "const-only predicate allowed", e2 == nil)
	assert(t, "empty visible set rejects column ref", errors.Is(e3, ErrInvisibleColumn))
	assert(t, "empty visible set prunes whole row", len(kept) == 0 && len(pruned) == 2)
	assert(t, "full visible set allows", e4 == nil)
	assert(t, "repeated column: 1 ref, 2 paths", errors.Is(e5, ErrInvisibleColumn) &&
		len(refs) == 1 && len(refs[0].Paths) == 2)
}

func TestFoldException(t *testing.T) {
	secret := predicate.Compare("secret", predicate.Eq, 1)
	cases := []struct {
		pred    *predicate.Node
		allowed bool
	}{
		{predicate.OrOf(secret, predicate.True()), true},
		{predicate.OrOf(predicate.True(), secret), true},
		{predicate.AndOf(secret, predicate.False()), true},
		{predicate.AndOf(predicate.False(), secret), true},
		{predicate.NotOf(predicate.OrOf(secret, predicate.True())), true},
		{predicate.OrOf(predicate.False(), secret), false},
		{predicate.OrOf(secret, predicate.False()), false},
		{predicate.AndOf(secret, predicate.True()), false},
	}
	for i, tc := range cases {
		_, err := testEngine().Check("analyst", tc.pred)
		assert(t, fmt.Sprintf("fold case %d", i), (err == nil) == tc.allowed)
	}
}

// TestExhaustive covers 8 visibility masks x 4 predicate forms (32 combos).
func TestExhaustive(t *testing.T) {
	cmp := func(c string) *predicate.Node { return predicate.Compare(c, predicate.Eq, 1) }
	forms := []struct {
		build func() *predicate.Node
		used  []string
	}{
		{func() *predicate.Node { return cmp("a") }, []string{"a"}},
		{func() *predicate.Node { return predicate.NotOf(cmp("a")) }, []string{"a"}},
		{func() *predicate.Node { return predicate.AndOf(cmp("a"), cmp("b"), cmp("c")) }, []string{"a", "b", "c"}},
		{func() *predicate.Node { return predicate.OrOf(cmp("a"), cmp("b"), cmp("c")) }, []string{"a", "b", "c"}},
	}
	for fi, f := range forms {
		for mask := 0; mask < 8; mask++ {
			var vis []string
			for i, c := range []string{"a", "b", "c"} {
				if mask&(1<<i) != 0 {
					vis = append(vis, c)
				}
			}
			pol := policy.New(map[string][]string{"r": vis})
			refs, err := NewEngine(pol).Check("r", f.build())
			wantReject := false
			for _, c := range f.used {
				if !pol.Visible("r", c) {
					wantReject = true
				}
			}
			assert(t, fmt.Sprintf("form %d mask %03b decision", fi, mask), errors.Is(err, ErrInvisibleColumn) == wantReject)
			for _, ref := range refs {
				assert(t, fmt.Sprintf("form %d mask %03b named correctly", fi, mask), !pol.Visible("r", ref.Col))
			}
		}
	}
}
