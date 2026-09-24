package filter

import (
	"errors"
	"fmt"
	"testing"

	"ontology/policy"
	"ontology/predicate"
)

func TestProbesRejected(t *testing.T) {
	f := New(policy.New(map[string][]string{"analyst": {"id", "name"}}))
	tests := []struct {
		name string
		pred *Node
	}{
		{"NOT(secret=1) must reject", predicate.Not(predicate.Eq("secret", "1"))},
		{"secret IS NULL must reject", predicate.IsNull("secret")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := f.Apply("analyst", tt.pred, []Row{{"id": "1"}})
			if !errors.Is(err, ErrInvisibleColumn) {
				t.Fatalf("err = %v, want ErrInvisibleColumn (no rows may leak)", err)
			}
			if res.Rows != nil {
				t.Fatalf("rejected query must return no rows, got %v", res.Rows)
			}
			if !res.Report.RejectedQuery || len(res.Report.Rejected) != 1 ||
				res.Report.Rejected[0].Column != "secret" {
				t.Fatalf("report must name secret once: %+v", res.Report.Rejected)
			}
		})
	}
}

func TestRejectPaths(t *testing.T) {
	f := New(policy.New(map[string][]string{"r": {"a"}}))
	tests := []struct {
		name string
		pred *Node
		want []string
	}{
		{
			"NOT path",
			predicate.Not(predicate.Eq("secret", "1")),
			[]string{"not>compare"},
		},
		{
			"OR branch + NOT path",
			predicate.Or(predicate.Eq("a", "1"),
				predicate.Not(predicate.IsNull("secret"))),
			[]string{"or[1]>not>compare"},
		},
		{
			"same column twice keeps both paths",
			predicate.And(predicate.Eq("secret", "1"),
				predicate.Not(predicate.IsNull("secret"))),
			[]string{"and[0]>compare", "and[1]>not>compare"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := f.Apply("r", tt.pred, nil)
			if !errors.Is(err, ErrInvisibleColumn) {
				t.Fatalf("err = %v", err)
			}
			refs := res.Report.Rejected
			if len(refs) != 1 || refs[0].Column != "secret" {
				t.Fatalf("refs = %+v", refs)
			}
			got := make([]string, len(refs[0].Paths))
			for i, p := range refs[0].Paths {
				got[i] = joinPath(p)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("paths = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestORConstantFoldException(t *testing.T) {
	f := New(policy.New(map[string][]string{"r": {"a"}}))
	tests := []struct {
		name      string
		pred      *Node
		wantAllow bool
		wantElide bool
	}{
		{
			"OR folds to true: hidden branch never read",
			predicate.Or(predicate.ConstNode(true), predicate.Eq("secret", "1")),
			true, true,
		},
		{
			"OR folds true; hidden branch nested under AND never read",
			predicate.Or(predicate.ConstNode(true),
				predicate.And(predicate.ConstNode(false), predicate.IsNull("secret"))),
			true, true,
		},
		{
			"OR does not fold: reject",
			predicate.Or(predicate.Eq("a", "1"), predicate.Eq("secret", "1")),
			false, false,
		},
		{
			"AND folds to false but still rejects (no AND exception)",
			predicate.And(predicate.ConstNode(false), predicate.Eq("secret", "1")),
			false, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := f.Apply("r", tt.pred, []Row{{"a": "x"}})
			allowed := err == nil
			if allowed != tt.wantAllow {
				t.Fatalf("allowed = %v (err=%v), want %v", allowed, err, tt.wantAllow)
			}
			elided := len(res.Report.Elided) == 1 && res.Report.Elided[0].Column == "secret"
			if elided != tt.wantElide {
				t.Fatalf("elided = %v, want %v (%s)", res.Report.Elided, tt.wantElide, res.Report.Encode())
			}
			if allowed && (len(res.Rows) != 1 || res.Rows[0]["a"] != "x") {
				t.Fatalf("allowed query must return rows: %v", res.Rows)
			}
		})
	}
}

func TestRowProjection(t *testing.T) {
	f := New(policy.New(map[string][]string{"r": {"id", "name"}}))
	rows := []Row{
		{"id": "1", "name": "bob", "secret": "S", "salary": "9"},
		{"name": "ann", "id": "2", "salary": "8", "secret": "T"},
	}
	res, err := f.Apply("r", nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range res.Rows {
		if len(row) != 2 {
			t.Fatalf("row %d has %d cols, want 2: %v", i, len(row), row)
		}
		if _, ok := row["secret"]; ok {
			t.Fatalf("secret physically present (zero/NULL would be indistinguishable): %v", row)
		}
		if _, ok := row["salary"]; ok {
			t.Fatalf("salary must be removed: %v", row)
		}
	}
	want := []Row{{"id": "1", "name": "bob"}, {"id": "2", "name": "ann"}}
	for i := range want {
		if fmt.Sprint(sortedKV(res.Rows[i])) != fmt.Sprint(sortedKV(want[i])) {
			t.Fatalf("row %d = %v, want %v", i, sortedKV(res.Rows[i]), sortedKV(want[i]))
		}
	}
	for _, col := range []string{"salary", "secret"} {
		if !contains(res.Report.DroppedColumns, col) {
			t.Fatalf("dropped report missing %s: %v", col, res.Report.DroppedColumns)
		}
	}
}

func TestCounters(t *testing.T) {
	f := New(policy.New(map[string][]string{"r": {"a"}}))
	pred := predicate.And(
		predicate.Or(predicate.Eq("a", "1"), predicate.Not(predicate.IsNull("b"))),
		predicate.Not(predicate.Eq("secret", "1")),
	)
	rows := []Row{{"a": "1", "b": "2", "secret": "3"}}
	res, _ := f.Apply("r", pred, rows)
	want := predicate.Count(pred)
	if f.NodesVisited() != want {
		t.Fatalf("nodes visited = %d, want total %d (single pass)", f.NodesVisited(), want)
	}
	if f.NodesVisited() != res.counters.NodesVisited {
		t.Fatal("counter mismatch")
	}

	big := make(Row, 1000)
	for i := 0; i < 1000; i++ {
		big[fmt.Sprintf("c%d", i)] = "v"
	}
	f2 := New(policy.New(map[string][]string{"r": {"c1", "c2", "c3", "c4", "c5"}}))
	if _, err := f2.Apply("r", nil, []Row{big}); err != nil {
		t.Fatal(err)
	}
	if got, bound := f2.CopyOps(), 4*5; got > bound {
		t.Fatalf("copy ops = %d, must be <= %d (O(visible))", got, bound)
	}
	if f2.CopyOps() != 5 {
		t.Fatalf("copy ops = %d, want exactly 5", f2.CopyOps())
	}
}

func TestEdgesAndErrors(t *testing.T) {
	pol := policy.New(map[string][]string{
		"blind": {},
		"all":   {"a", "b", "secret"},
	})
	f := New(pol)
	if _, err := f.Apply("ghost", nil, nil); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("err = %v, want ErrUnknownRole", err)
	}
	if _, err := f.Apply("blind", predicate.Eq("a", "1"), nil); !errors.Is(err, ErrEmptyRole) {
		t.Fatalf("err = %v, want ErrEmptyRole", err)
	}
	res, err := f.Apply("blind", nil, []Row{{"a": "1"}})
	if err != nil || len(res.Rows) != 1 || len(res.Rows[0]) != 0 {
		t.Fatalf("empty-visible no-predicate row must be empty: %v %v", res, err)
	}
	allRes, err := f.Apply("all", predicate.Eq("secret", "1"), []Row{{"secret": "1"}})
	if err != nil || len(allRes.Rows[0]) != 3-2 || allRes.Rows[0]["secret"] != "1" {
		t.Fatalf("full-visible role must see secret: %v %v", allRes, err)
	}
	constRes, err := f.Apply("blind", predicate.ConstNode(true), []Row{{"a": "1"}})
	if err != nil || len(constRes.Rows[0]) != 0 || len(constRes.Report.DroppedColumns) != 1 {
		t.Fatalf("const predicate references nothing: allow with empty projection: %v %v", constRes, err)
	}
	empty, err := f.Apply("all", nil, nil)
	if err != nil || len(empty.Rows) != 0 || empty.Report.Encode() == "" {
		t.Fatalf("empty input must give empty result + report: %v %v", empty, err)
	}
}

func TestExhaustive32(t *testing.T) {
	cols := [3]string{"a", "b", "secret"}
	forms := map[string]func() *Node{
		"=":   func() *Node { return predicate.Eq("secret", "1") },
		"NOT": func() *Node { return predicate.Not(predicate.Eq("secret", "1")) },
		"AND": func() *Node {
			return predicate.And(predicate.Eq("a", "1"), predicate.Eq("secret", "1"))
		},
		"OR": func() *Node {
			return predicate.Or(predicate.Eq("a", "1"), predicate.Eq("secret", "1"))
		},
	}
	// 各形态引用的列；拒绝与否只取决于这些列是否全部可见。
	formCols := map[string][][3]bool{
		"=":   {{false, false, true}},
		"NOT": {{false, false, true}},
		"AND": {{true, false, true}},
		"OR":  {{true, false, true}},
	}
	summary := map[string][2]int{}
	for mask := 0; mask < 8; mask++ {
		var vis []string
		for i := range cols {
			if mask&(1<<i) != 0 {
				vis = append(vis, cols[i])
			}
		}
		f := New(policy.New(map[string][]string{"r": vis}))
		formNames := []string{"=", "NOT", "AND", "OR"}
		for _, name := range formNames {
			build := forms[name]
			pred := build()
			_, err := f.Apply("r", pred, nil)
			rejected := errors.Is(err, ErrInvisibleColumn) || errors.Is(err, ErrEmptyRole)
			wantReject := false
			for _, ref := range formCols[name] {
				for i := range ref {
					if ref[i] && mask&(1<<i) == 0 {
						wantReject = true
					}
				}
			}
			if rejected != wantReject {
				t.Fatalf("form=%s vis=%v: rejected=%v want=%v",
					name, vis, rejected, wantReject)
			}
			s := summary[name]
			if wantReject {
				s[1]++
			} else {
				s[0]++
			}
			summary[name] = s
		}
	}
	for name, s := range summary {
		if s[0]+s[1] != 8 {
			t.Fatalf("form %s covered %d, want 8", name, s[0]+s[1])
		}
		t.Logf("form %s: allow=%d reject=%d", name, s[0], s[1])
	}
}

func joinPath(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += ">"
		}
		out += s
	}
	return out
}

func sortedKV(row Row) []string {
	out := make([]string, 0, len(row))
	for k, v := range row {
		out = append(out, k+"="+v)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
