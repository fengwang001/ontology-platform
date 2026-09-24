package push_test

import (
	"strings"
	"testing"

	"ontology/ast"
	"ontology/push"
)

func plan3() *push.Plan {
	return push.Join(push.Scan("t1"), push.Join(push.Scan("t2"), push.Scan("t3")))
}

func TestPush(t *testing.T) {
	eq := ast.Eq(ast.Col("t1", "a"), ast.Const(ast.True))
	ne := ast.Ne(ast.Col("t2", "c"), ast.Col("t2", "d"))
	join := ast.Eq(ast.Col("t1", "b"), ast.Col("t3", "e"))
	cases := []struct {
		name    string
		plan    *push.Plan
		pred    *ast.Node
		wantErr string
		want    string
	}{
		{"single-table conjuncts land on scans", plan3(), ast.And(eq, ne), "",
			"join(join(scan(t2 | (<> t2.c t2.d)), scan(t3)), scan(t1 | (= t1.a true)))"},
		{"cross-table conjunct stays at join", plan3(), ast.And(eq, join), "",
			"join(join(scan(t2), scan(t3)), scan(t1 | (= t1.a true)) | (= t1.b t3.e))"},
		{"degenerate 1-kid AND is pushed", plan3(), ast.And(ne), "",
			"join(join(scan(t2 | (<> t2.c t2.d)), scan(t3)), scan(t1))"},
		{"unknown table is decidable error", plan3(),
			ast.Eq(ast.Col("t9", "z"), ast.Const(ast.True)), "unknown table t9", ""},
		{"nil predicate is decidable error", plan3(), nil, "nil", ""},
		{"empty AND is decidable error", plan3(), ast.And(), "no kids", ""},
	}
	for _, tc := range cases {
		err := push.Push(tc.plan, tc.pred)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if cerr := push.Check(tc.plan); cerr != nil {
			t.Errorf("%s: check: %v", tc.name, cerr)
		}
		if got := tc.plan.String(); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name    string
		plan    *push.Plan
		wantErr string
	}{
		{"clean plan passes", plan3(), ""},
		{"planted cross-table ref fails", &push.Plan{
			Table: "t1",
			Pred:  ast.Eq(ast.Col("t1", "a"), ast.Col("t2", "c")),
		}, "foreign table t2"},
	}
	for _, tc := range cases {
		err := push.Check(tc.plan)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}
