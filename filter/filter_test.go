package filter

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"ontology/policy"
	"ontology/predicate"
)

func pol(cols ...string) policy.Policy { return policy.New("r", cols) }

func cmp(col string) *predicate.Node { return predicate.Cmp(col, predicate.Eq, 1) }

func render(m map[string]any) string {
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, "|")
}

// 甲、乙 counterexamples: both probes must be rejected, never answered.
func TestProbesRejected(t *testing.T) {
	rows := []map[string]any{{"id": 1, "secret": 1}, {"id": 2, "secret": 2}}
	probes := map[string]*predicate.Node{
		"NOT(secret=1)":  predicate.NotNode(cmp("secret")),
		"secret IS NULL": predicate.Cmp("secret", predicate.IsNull, nil),
	}
	for name, pred := range probes {
		t.Run(name, func(t *testing.T) {
			out, refs, err := Query(rows, pred, pol("id"))
			if !errors.Is(err, ErrInvisibleColumn) {
				t.Fatalf("want ErrInvisibleColumn, got %v", err)
			}
			if out != nil {
				t.Fatalf("probe returned rows %v — leaks like 甲/乙", out)
			}
			if len(refs) != 1 || refs[0].Column != "secret" || refs[0].Path == "" {
				t.Fatalf("refs must name secret with a path, got %+v", refs)
			}
		})
	}
}

// OR/AND short-circuit exception per DESIGN.md: folded-away references
// are not read; surviving ones are rejected.
func TestConstantFoldingException(t *testing.T) {
	T, F := predicate.Boolean(true), predicate.Boolean(false)
	cases := []struct {
		name    string
		pred    *predicate.Node
		wantErr bool
	}{
		{"TRUE OR secret=1", predicate.OrAll(T, cmp("secret")), false},
		{"FALSE AND secret=1", predicate.AndAll(F, cmp("secret")), false},
		{"FALSE OR secret=1", predicate.OrAll(F, cmp("secret")), true},
		{"TRUE AND secret=1", predicate.AndAll(T, cmp("secret")), true},
		{"NOT TRUE keeps ref alive", predicate.OrAll(predicate.NotNode(T), cmp("secret")), true},
		{"nested fold kills ref", predicate.AndAll(predicate.OrAll(T, cmp("secret")), cmp("id")), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewChecker(pol("id")).Check(c.pred)
			if got := errors.Is(err, ErrInvisibleColumn); got != c.wantErr {
				t.Fatalf("rejected=%v, want %v (err=%v)", got, c.wantErr, err)
			}
		})
	}
}

func TestSingleTraversal(t *testing.T) {
	trees := []*predicate.Node{
		cmp("a"),
		predicate.NotNode(predicate.Cmp("a", predicate.Gt, 2)),
		predicate.AndAll(cmp("a"), predicate.OrAll(predicate.Cmp("b", predicate.Lt, 5),
			predicate.Boolean(true)), predicate.NotNode(predicate.Cmp("c", predicate.IsNotNul, nil))),
	}
	for i, tr := range trees {
		c := NewChecker(pol("a", "b", "c"))
		if err := c.Check(tr); err != nil {
			t.Fatalf("tree %d: %v", i, err)
		}
		if c.Visited() != predicate.NodeCount(tr) {
			t.Fatalf("tree %d: visited %d, want %d", i, c.Visited(), predicate.NodeCount(tr))
		}
	}
}

func TestPruneRowBound(t *testing.T) {
	row := map[string]any{}
	for i := 0; i < 1000; i++ {
		row[fmt.Sprintf("c%03d", i)] = i
	}
	p := pol("c000", "c001", "c002", "c003", "c004")
	out, copies := PruneRow(row, p)
	if copies > 4*5 {
		t.Fatalf("copies %d exceed bound %d", copies, 4*5)
	}
	if got, want := render(out), "c000=0|c001=1|c002=2|c003=3|c004=4"; got != want {
		t.Fatalf("pruned row bytes %q, want %q", got, want)
	}
	for k := range out {
		if !p.Visible(k) {
			t.Fatalf("invisible column %q survived pruning", k)
		}
	}
}

// 8 visibility masks x 4 predicate forms = 32 combos, checked in a loop.
func TestExhaustive32(t *testing.T) {
	tri := func(j func(...*predicate.Node) *predicate.Node) func() *predicate.Node {
		return func() *predicate.Node {
			return j(predicate.Cmp("a", predicate.Eq, 1), predicate.Cmp("b", predicate.Eq, 2),
				predicate.Cmp("c", predicate.Eq, 3))
		}
	}
	needs := [][]string{{"a"}, {"a"}, {"a", "b", "c"}, {"a", "b", "c"}}
	makers := []func() *predicate.Node{
		func() *predicate.Node { return cmp("a") },
		func() *predicate.Node { return predicate.NotNode(cmp("a")) },
		tri(predicate.AndAll), tri(predicate.OrAll),
	}
	passes, rejects := 0, 0
	for mask := 0; mask < 8; mask++ {
		var vis []string
		for i, col := range []string{"a", "b", "c"} {
			if mask&(1<<i) != 0 {
				vis = append(vis, col)
			}
		}
		for i, make := range makers {
			want := true
			for _, col := range needs[i] {
				want = want && slices.Contains(vis, col)
			}
			err := NewChecker(policy.New("r", vis)).Check(make())
			if got := err == nil; got != want {
				t.Fatalf("mask=%03b need=%v: allowed=%v, want %v", mask, needs[i], got, want)
			}
			if err == nil {
				passes++
			} else {
				rejects++
			}
		}
	}
	if passes != 10 || rejects != 22 {
		t.Fatalf("passes=%d rejects=%d, want 10/22", passes, rejects)
	}
}

func TestEdges(t *testing.T) {
	c := NewChecker(pol())
	if err := c.Check(nil); err != nil || c.Visited() != 0 {
		t.Fatalf("nil predicate: err=%v visited=%d", err, c.Visited())
	}
	constOnly := predicate.OrAll(predicate.Boolean(true), cmp("secret"))
	if err := NewChecker(pol()).Check(constOnly); err != nil {
		t.Fatalf("const-folded predicate on empty policy: %v", err)
	}
	if err := NewChecker(pol()).Check(cmp("id")); err == nil {
		t.Fatal("empty policy must reject any column reference")
	}
	if err := NewChecker(pol("id", "secret")).Check(predicate.NotNode(cmp("secret"))); err != nil {
		t.Fatalf("full policy: %v", err)
	}
	dup := predicate.AndAll(cmp("s"), predicate.OrAll(cmp("s"), cmp("id")))
	var rej *Rejection
	if err := NewChecker(pol("id")).Check(dup); !errors.As(err, &rej) {
		t.Fatalf("duplicate refs: %v", err)
	} else if len(rej.Refs) != 2 || rej.Refs[0].Path != "$/AND[0]" ||
		rej.Refs[1].Path != "$/AND[1]/OR[0]" {
		t.Fatalf("duplicate column must report every path, got %+v", rej.Refs)
	}
	rows := []map[string]any{{"id": 1, "secret": 9}, {"id": 2, "secret": 8}}
	out, _, err := Query(rows, predicate.Cmp("id", predicate.Gt, 1), pol("id"))
	if err != nil || len(out) != 1 || render(out[0]) != "id=2" {
		t.Fatalf("query: out=%v err=%v", out, err)
	}
}

func TestErrorKindsDistinguishable(t *testing.T) {
	inv := NewChecker(pol()).Check(cmp("x"))
	if !errors.Is(inv, ErrInvisibleColumn) || errors.Is(inv, ErrMissingColumn) || errors.Is(inv, ErrInvalidPredicate) {
		t.Fatalf("invisible error misclassified: %v", inv)
	}
	bad := NewChecker(pol("x")).Check(&predicate.Node{Kind: predicate.And})
	if !errors.Is(bad, ErrInvalidPredicate) || errors.Is(bad, ErrInvisibleColumn) {
		t.Fatalf("invalid error misclassified: %v", bad)
	}
	_, _, miss := Query([]map[string]any{{"a": 1}}, cmp("b"), pol("b"))
	if !errors.Is(miss, ErrMissingColumn) || errors.Is(miss, ErrInvisibleColumn) {
		t.Fatalf("missing error misclassified: %v", miss)
	}
}
