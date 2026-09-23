package table_test

import (
	"slices"
	"testing"

	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
	"ontology/table"
)

func probe(tb *table.Table, names ...string) []resolve.Result {
	var out []resolve.Result
	for _, n := range names {
		r, _ := tb.Ref(name.Name(n), 99)
		out = append(out, r)
	}
	return out
}

func TestLeaveRestoresOuter(t *testing.T) {
	tb := table.New(4, 8)
	_ = tb.Declare("a", name.KindStrict, 1)
	_ = tb.Declare("b", name.KindForward, 2)
	before := probe(tb, "a", "b")
	_ = tb.Enter()
	_ = tb.Declare("a", name.KindStrict, 3)
	if r, _ := tb.Ref("a", 4); r.Depth != 1 {
		t.Fatalf("shadow failed: depth=%d", r.Depth)
	}
	_ = tb.Leave()
	if after := probe(tb, "a", "b"); !slices.Equal(before, after) {
		t.Fatalf("outer changed: %v -> %v", before, after)
	}
}

func TestRefOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		run   func(tb *table.Table) (resolve.Result, error)
		err   error
		depth int
		pos   int
	}{
		{"basic", func(tb *table.Table) (resolve.Result, error) {
			_ = tb.Declare("a", name.KindStrict, 1)
			return tb.Ref("a", 2)
		}, nil, 0, 1},
		{"shadow", func(tb *table.Table) (resolve.Result, error) {
			_ = tb.Declare("a", name.KindStrict, 1)
			_ = tb.Enter()
			_ = tb.Declare("a", name.KindStrict, 3)
			return tb.Ref("a", 4)
		}, nil, 1, 3},
		{"forward before decl", func(tb *table.Table) (resolve.Result, error) {
			_ = tb.Declare("f", name.KindForward, 5)
			return tb.Ref("f", 2)
		}, nil, 0, 5},
		{"strict before decl", func(tb *table.Table) (resolve.Result, error) {
			_ = tb.Declare("g", name.KindStrict, 5)
			return tb.Ref("g", 2)
		}, resolve.ErrUseBeforeDeclare, 0, 0},
		{"undefined", func(tb *table.Table) (resolve.Result, error) {
			return tb.Ref("nope", 2)
		}, resolve.ErrUndefined, 0, 0},
		{"shadow+forward: inner wins", func(tb *table.Table) (resolve.Result, error) {
			_ = tb.Declare("x", name.KindStrict, 1)
			_ = tb.Enter()
			_ = tb.Declare("x", name.KindForward, 5)
			return tb.Ref("x", 2)
		}, nil, 1, 5},
		{"shadow+strict: local error, not outer", func(tb *table.Table) (resolve.Result, error) {
			_ = tb.Declare("x", name.KindStrict, 1)
			_ = tb.Enter()
			_ = tb.Declare("x", name.KindStrict, 5)
			return tb.Ref("x", 2)
		}, resolve.ErrUseBeforeDeclare, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.run(table.New(8, 8))
			if err != tc.err {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
			if tc.err == nil && (r.Depth != tc.depth || r.Decl.Pos != tc.pos) {
				t.Fatalf("got depth=%d pos=%d, want %d/%d", r.Depth, r.Decl.Pos, tc.depth, tc.pos)
			}
		})
	}
}

func TestDuplicateDeclare(t *testing.T) {
	for _, k := range []name.Kind{name.KindStrict, name.KindForward} {
		tb := table.New(4, 4)
		_ = tb.Declare("a", k, 1)
		first, _ := tb.Ref("a", 2)
		if err := tb.Declare("a", k, 3); err != scope.ErrDuplicateDeclare {
			t.Fatalf("kind=%d: err=%v", k, err)
		}
		if again, _ := tb.Ref("a", 4); again != first || again.Decl.Pos != 1 {
			t.Fatalf("kind=%d: earlier ref unstable: %v", k, again)
		}
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	noop := func(tb *table.Table) {}
	cases := []struct {
		name  string
		maxD  int
		setup func(tb *table.Table)
		op    func(tb *table.Table) error
		want  error
	}{
		{"dup declare", 2, noop, func(tb *table.Table) error { return tb.Declare("a", name.KindStrict, 2) }, scope.ErrDuplicateDeclare},
		{"decl limit", 1, noop, func(tb *table.Table) error { return tb.Declare("b", name.KindStrict, 2) }, table.ErrDeclLimit},
		{"depth limit", 1, func(tb *table.Table) { _ = tb.Enter() }, func(tb *table.Table) error { return tb.Enter() }, table.ErrDepthLimit},
		{"leave root", 1, noop, func(tb *table.Table) error { return tb.Leave() }, table.ErrLeaveRoot},
		{"empty declare", 1, noop, func(tb *table.Table) error { return tb.Declare("", name.KindStrict, 2) }, table.ErrEmptyName},
		{"empty ref", 1, noop, func(tb *table.Table) error { _, e := tb.Ref("", 2); return e }, table.ErrEmptyName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := table.New(1, tc.maxD)
			_ = tb.Declare("a", name.KindStrict, 1)
			tc.setup(tb)
			before := probe(tb, "a")
			if err := tc.op(tb); err != tc.want {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if after := probe(tb, "a"); !slices.Equal(before, after) || tb.SelfCheck() != nil {
				t.Fatalf("rejected op left trace")
			}
		})
	}
}
