package table_test

import (
	"errors"
	"testing"

	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
	"ontology/table"
)

func mustRef(t *testing.T, tb *table.Table, n string, pos int) (name.Decl, int) {
	t.Helper()
	d, depth, err := tb.Ref(n, pos)
	if err != nil {
		t.Fatalf("Ref(%q,%d): %v", n, pos, err)
	}
	return d, depth
}

func TestBasicShadowForward(t *testing.T) {
	cases := []struct {
		label     string
		refName   string
		refPos    int
		wantDepth int
		wantPos   int
		wantErr   error
	}{
		{"root decl hit", "p", 10, 0, 10, nil},
		{"inner shadows outer", "a", 20, 1, 15, nil},
		{"inner fwd decl wins before its pos", "a", 5, 1, 15, nil},
		{"outer only, forward allowed", "f", 2, 0, 10, nil},
		{"outer plain, use before decl", "p", 2, 0, 0, resolve.ErrUseBeforeDecl},
		{"undefined", "zzz", 0, 0, 0, resolve.ErrUndefined},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			tb := table.New(10, 10)
			_ = tb.Declare("a", name.Plain, 0)
			_ = tb.Declare("f", name.Forward, 10)
			_ = tb.Declare("p", name.Plain, 10)
			_ = tb.Enter()
			_ = tb.Declare("a", name.Forward, 15)
			d, depth, err := tb.Ref(tc.refName, tc.refPos)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (depth != tc.wantDepth || d.Pos != tc.wantPos) {
				t.Fatalf("depth=%d pos=%d want %d/%d", depth, d.Pos, tc.wantDepth, tc.wantPos)
			}
			if err := tb.SelfCheck(); err != nil {
				t.Fatalf("SelfCheck: %v", err)
			}
		})
	}
}

func TestDuplicateDeclare(t *testing.T) {
	tb := table.New(10, 10)
	_ = tb.Declare("x", name.Plain, 1)
	if err := tb.Declare("x", name.Forward, 9); !errors.Is(err, scope.ErrDuplicate) {
		t.Fatalf("err=%v want ErrDuplicate", err)
	}
	d, _ := mustRef(t, tb, "x", 2)
	if d != (name.Decl{Name: "x", Kind: name.Plain, Pos: 1}) {
		t.Fatalf("first declaration not intact: %+v", d)
	}
}

func TestLeaveRestores(t *testing.T) {
	tb := table.New(10, 10)
	_ = tb.Declare("x", name.Plain, 1)
	_ = tb.Declare("y", name.Forward, 2)
	bx, bd, be := tb.Ref("x", 3)
	by, byd, bye := tb.Ref("y", 0)
	_ = tb.Enter()
	_ = tb.Declare("x", name.Forward, 9)
	_, _, _ = tb.Ref("x", 0)
	_ = tb.Leave()
	ax, ad, ae := tb.Ref("x", 3)
	ay, ayd, aye := tb.Ref("y", 0)
	if bx != ax || bd != ad || be != ae || by != ay || byd != ayd || bye != aye {
		t.Fatal("outer scope not restored field-by-field after Leave")
	}
	if err := tb.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestRejectedOpsKeepState(t *testing.T) {
	cases := []struct {
		label string
		setup func() *table.Table
		op    func(*table.Table) error
		want  error
	}{
		{"leave at root", func() *table.Table { return table.New(2, 2) },
			func(tb *table.Table) error { return tb.Leave() }, table.ErrLeaveRoot},
		{"depth limit", func() *table.Table { tb := table.New(1, 2); _ = tb.Enter(); return tb },
			func(tb *table.Table) error { return tb.Enter() }, table.ErrMaxDepth},
		{"decl limit", func() *table.Table { tb := table.New(2, 1); _ = tb.Declare("a", name.Plain, 0); return tb },
			func(tb *table.Table) error { return tb.Declare("b", name.Plain, 1) }, table.ErrMaxDecls},
		{"empty declare", func() *table.Table { return table.New(2, 2) },
			func(tb *table.Table) error { return tb.Declare("", name.Plain, 0) }, table.ErrEmptyName},
		{"empty ref", func() *table.Table { return table.New(2, 2) },
			func(tb *table.Table) error { _, _, err := tb.Ref("", 0); return err }, table.ErrEmptyName},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			tb := tc.setup()
			_ = tb.Declare("keep", name.Plain, 0)
			before, bd, _ := tb.Ref("keep", 1)
			if err := tc.op(tb); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			after, ad, _ := tb.Ref("keep", 1)
			if before != after || bd != ad || tb.SelfCheck() != nil {
				t.Fatal("rejected op changed observable state")
			}
		})
	}
}

func TestCapturesExact(t *testing.T) {
	tb := table.New(10, 10)
	_ = tb.Declare("a", name.Plain, 0)
	_ = tb.Declare("b", name.Plain, 0)
	_ = tb.Enter()
	_, _, _ = tb.Ref("a", 1)
	_, _, _ = tb.Ref("a", 2)
	_ = tb.Declare("c", name.Forward, 9)
	_, _, _ = tb.Ref("c", 1)
	if caps := tb.Captures(); len(caps) != 1 || caps[0].Decl.Name != "a" || caps[0].Depth != 0 {
		t.Fatalf("captures=%+v want exactly [a@0]", caps)
	}
}

func TestErrorsDistinct(t *testing.T) {
	errs := []error{resolve.ErrUndefined, resolve.ErrUseBeforeDecl, scope.ErrDuplicate,
		table.ErrEmptyName, table.ErrLeaveRoot, table.ErrMaxDepth, table.ErrMaxDecls}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("errors %d and %d not distinct: %v vs %v", i, j, a, b)
			}
		}
	}
}
