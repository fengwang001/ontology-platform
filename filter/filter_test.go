package filter

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"ontology/policy"
	"ontology/predicate"
)

func pol1() *policy.Policy {
	return policy.New([]string{"a", "b", "secret"}, map[string][]string{
		"user": {"a", "b"},
		"none": nil,
		"all":  {"a", "b", "secret"},
	})
}

func rejectKind(t *testing.T, err error, want error) []Ref {
	t.Helper()
	if err == nil {
		t.Fatalf("want reject %v, got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("errors.Is = false, want %v; got %v", want, err)
	}
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %T", err)
	}
	return re.Refs
}

func TestProbesRejected(t *testing.T) {
	tests := []struct {
		name string
		pred predicate.Node
		path string
	}{
		{"NOT(secret=1)", predicate.Not{X: predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1}}, "$/not/cmp"},
		{"secret IS NULL", predicate.Cmp{Col: "secret", Op: predicate.OpIsNull}, "$/cmp"},
	}
	p := pol1()
	rows := []map[string]any{{"a": 1, "b": 2, "secret": 1}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Compile(p, "user", tt.pred)
			refs := rejectKind(t, err, ErrHiddenColumn)
			if refs[0].Col != "secret" || refs[0].Path != tt.path {
				t.Fatalf("ref = %+v, want secret @ %s", refs[0], tt.path)
			}
			if c != nil {
				t.Fatalf("compiled plan must be nil on reject")
			}
			// 反例断言：甲/乙做法会给出的“放行”答案，本实现不得给出。
			safe := func() bool {
				cc, e := Compile(p, "user", tt.pred)
				return e == nil && len(cc.Apply(rows)) > 0
			}()
			if safe {
				t.Fatalf("implementation leaks like approach 甲/乙: rows returned")
			}
		})
	}
}

func TestCompileAndProject(t *testing.T) {
	p := pol1()
	tests := []struct {
		name   string
		role   string
		pred   predicate.Node
		err    error
		visits int
	}{
		{"visible cmp ok", "user", predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1}, nil, 1},
		{"nil predicate", "user", nil, nil, 1},
		{"unknown column", "user", predicate.Cmp{Col: "ghost"}, ErrUnknownColumn, 1},
		{"invalid empty and", "all", predicate.And{}, ErrInvalidPredicate, 1},
		{"empty visibility rejects", "none", predicate.Cmp{Col: "a"}, ErrHiddenColumn, 1},
		{"full visibility ok", "all", predicate.Cmp{Col: "secret", Op: predicate.OpIsNull}, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Compile(p, tt.role, tt.pred)
			if tt.err != nil {
				rejectKind(t, err, tt.err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.NodesVisited() != tt.visits {
				t.Fatalf("visits = %d, want %d", c.NodesVisited(), tt.visits)
			}
		})
	}
}

func TestProjectionRemovesHidden(t *testing.T) {
	p := pol1()
	c, err := Compile(p, "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"a": 1, "b": nil, "secret": "shh"}
	got := c.Project(row)
	want := map[string]any{"a": 1, "b": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projected = %v, want %v", got, want)
	}
	if _, leak := got["secret"]; leak {
		t.Fatalf("hidden column key must be absent, not zeroed/null")
	}
	// 不可见与值为空可区分：b 可见且显式为 NULL 仍然存在。
	if b, ok := got["b"]; !ok || b != nil {
		t.Fatalf("visible NULL column must survive projection")
	}
}

func TestNodeCountSinglePass(t *testing.T) {
	p := pol1()
	tree := predicate.And{Xs: []predicate.Node{
		predicate.Or{Xs: []predicate.Node{
			predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1},
			predicate.Not{X: predicate.Cmp{Col: "b", Op: predicate.OpIsNull}},
		}},
		predicate.Cmp{Col: "b", Op: predicate.OpEq, Value: 2},
	}}
	c, err := Compile(p, "user", tree)
	if err != nil {
		t.Fatal(err)
	}
	if c.NodesVisited() != predicate.Count(tree) {
		t.Fatalf("visits %d != nodes %d", c.NodesVisited(), predicate.Count(tree))
	}
}

func TestORShortCircuitException(t *testing.T) {
	p := pol1()
	tests := []struct {
		name string
		pred predicate.Node
		err  error
	}{
		{"false const arm with hidden cmp ignored",
			predicate.Or{Xs: []predicate.Node{
				predicate.Const{Value: false},
				predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1},
			}}, nil},
		{"true const arm dominates whole OR",
			predicate.Or{Xs: []predicate.Node{
				predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1},
				predicate.Const{Value: true},
			}}, nil},
		{"non-constant hidden arm still rejected",
			predicate.Or{Xs: []predicate.Node{
				predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1},
				predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1},
			}}, ErrHiddenColumn},
		{"hidden cmp under not-const rejected",
			predicate.Not{X: predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1}}, ErrHiddenColumn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile(p, "user", tt.pred)
			if tt.err == nil && err != nil {
				t.Fatalf("unexpected reject: %v", err)
			}
			if tt.err != nil {
				rejectKind(t, err, tt.err)
			}
		})
	}
}

func TestDuplicateColumnAllPathsReported(t *testing.T) {
	p := pol1()
	tree := predicate.Or{Xs: []predicate.Node{
		predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1},
		predicate.Not{X: predicate.Cmp{Col: "secret", Op: predicate.OpIsNull}},
	}}
	_, err := Compile(p, "user", tree)
	refs := rejectKind(t, err, ErrHiddenColumn)
	wantPaths := []string{"$/or[0]/cmp", "$/or[1]/not/cmp"}
	if len(refs) != 2 {
		t.Fatalf("refs = %v, want both paths", refs)
	}
	for i, r := range refs {
		if r.Col != "secret" || r.Path != wantPaths[i] {
			t.Fatalf("ref %d = %+v, want path %s", i, r, wantPaths[i])
		}
	}
}

func TestCopyOpsBoundThousandRows(t *testing.T) {
	cols := make([]string, 1000)
	for i := range cols {
		cols[i] = "c" + strconv.Itoa(i)
	}
	vis := []string{cols[1], cols[100], cols[500], cols[777], cols[999]}
	p := policy.New(cols, map[string][]string{"r": vis})
	c, err := Compile(p, "r", nil)
	if err != nil {
		t.Fatal(err)
	}
	row := make(map[string]any, 1000)
	for _, col := range cols {
		row[col] = 1
	}
	_ = c.Project(row)
	if got := c.CopyOps(); got > 4*5 {
		t.Fatalf("copy ops = %d, bound %d", got, 4*5)
	}
}

func TestExhaustive32(t *testing.T) {
	eq := func(col string, v int64) predicate.Node {
		return predicate.Cmp{Col: col, Op: predicate.OpEq, Value: v}
	}
	forms := map[string]func() predicate.Node{
		"=":   func() predicate.Node { return eq("a", 1) },
		"NOT": func() predicate.Node { return predicate.Not{X: eq("a", 1)} },
		"AND": func() predicate.Node {
			return predicate.And{Xs: []predicate.Node{eq("a", 1), eq("b", 2)}}
		},
		"OR": func() predicate.Node {
			return predicate.Or{Xs: []predicate.Node{eq("a", 1), eq("b", 2)}}
		},
	}
	formRefs := map[string][]string{
		"=": {"a"}, "NOT": {"a"}, "AND": {"a", "b"}, "OR": {"a", "b"},
	}
	summary := map[string][2]int{} // form -> [allow, reject]
	for mask := 0; mask < 8; mask++ {
		vis := map[string][]string{"r": {}}
		hidden := map[string]bool{}
		for i, col := range []string{"a", "b", "c"} {
			if mask&(1<<i) == 0 {
				hidden[col] = true
			} else {
				vis["r"] = append(vis["r"], col)
			}
		}
		pp := policy.New([]string{"a", "b", "c"}, vis)
		for name, build := range forms {
			_, err := Compile(pp, "r", build())
			wantReject := false
			for _, ref := range formRefs[name] {
				if hidden[ref] {
					wantReject = true
				}
			}
			gotReject := errors.Is(err, ErrHiddenColumn)
			if gotReject != wantReject {
				t.Fatalf("mask=%03b form=%s reject=%v want %v", mask, name, gotReject, wantReject)
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
	wantSummary := map[string][2]int{
		"=": {4, 4}, "NOT": {4, 4}, "AND": {2, 6}, "OR": {2, 6},
	}
	if !reflect.DeepEqual(summary, wantSummary) {
		t.Fatalf("summary = %v, want %v", summary, wantSummary)
	}
}
