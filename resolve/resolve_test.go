package resolve_test

import (
	"fmt"
	"testing"

	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
)

func decl(n string, k name.Kind, pos int) name.Decl {
	return name.Decl{Name: name.Name(n), Kind: k, Pos: pos}
}

func TestResolveOutcomes(t *testing.T) {
	root := scope.New(nil, 0)
	_ = root.Declare(decl("o", name.KindStrict, 1))
	_ = root.Declare(decl("sh", name.KindStrict, 1))
	kid := scope.New(root, 1)
	_ = kid.Declare(decl("x", name.KindStrict, 5))
	_ = kid.Declare(decl("xf", name.KindForward, 5))
	_ = kid.Declare(decl("sh", name.KindForward, 9))
	cases := []struct {
		name      string
		ref       resolve.Ref
		wantErr   error
		wantDepth int
		wantPos   int
	}{
		{"outer strict after decl", resolve.Ref{Scope: kid, Name: "o", Pos: 2}, nil, 0, 1},
		{"outer strict before decl", resolve.Ref{Scope: kid, Name: "o", Pos: 0}, resolve.ErrUseBeforeDeclare, 0, 0},
		{"inner strict before decl", resolve.Ref{Scope: kid, Name: "x", Pos: 2}, resolve.ErrUseBeforeDeclare, 0, 0},
		{"inner forward before decl", resolve.Ref{Scope: kid, Name: "xf", Pos: 2}, nil, 1, 5},
		{"shadow: inner forward beats outer", resolve.Ref{Scope: kid, Name: "sh", Pos: 2}, nil, 1, 9},
		{"undefined", resolve.Ref{Scope: kid, Name: "zzz", Pos: 9}, resolve.ErrUndefined, 0, 0},
	}
	res := resolve.NewResolver()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := res.Resolve(tc.ref)
			if err != tc.wantErr {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (r.Depth != tc.wantDepth || r.Decl.Pos != tc.wantPos) {
				t.Fatalf("got depth=%d pos=%d, want depth=%d pos=%d",
					r.Depth, r.Decl.Pos, tc.wantDepth, tc.wantPos)
			}
		})
	}
}

func buildChain(depth, junk int, inner bool) *scope.Scope {
	root := scope.New(nil, 0)
	if !inner {
		_ = root.Declare(decl("target", name.KindStrict, 0))
	}
	cur := root
	for d := 1; d <= depth; d++ {
		cur = scope.New(cur, d)
		for i := 0; i < junk; i++ {
			_ = cur.Declare(decl(fmt.Sprintf("j%d_%d", d, i), name.KindStrict, 0))
		}
	}
	if inner {
		_ = cur.Declare(decl("target", name.KindStrict, 0))
	}
	return cur
}

func TestLookupComplexity(t *testing.T) {
	cases := []struct {
		name      string
		inner     bool
		bound     int64
		wantDepth int
	}{
		{"target-at-outermost: bound depth+1", false, 1001, 0},
		{"target-at-innermost: bound small const", true, 4, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := resolve.NewResolver()
			r, err := res.Resolve(resolve.Ref{Scope: buildChain(1000, 50, tc.inner), Name: "target", Pos: 1})
			if err != nil || r.Depth != tc.wantDepth {
				t.Fatalf("resolve failed: err=%v depth=%d", err, r.Depth)
			}
			if got := res.Looked(); got > tc.bound {
				t.Fatalf("looked %d entries, bound %d", got, tc.bound)
			}
			t.Logf("looked=%d (bound %d)", res.Looked(), tc.bound)
		})
	}
}
