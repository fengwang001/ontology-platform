package resolve

import (
	"errors"
	"fmt"
	"testing"

	"ontology/name"
	"ontology/scope"
)

// buildChain creates depth scopes, each with per filler declarations,
// plus the target declaration at targetDepth. Returns the innermost.
func buildChain(depth, per int, target string, targetDepth int) *scope.Scope {
	root := scope.New(nil, 0)
	cur := root
	for d := 0; d < depth; d++ {
		if d > 0 {
			cur = scope.New(cur, d)
		}
		for j := 0; j < per; j++ {
			_ = cur.Declare(name.Decl{Name: fmt.Sprintf("f%d-%d", d, j), Kind: name.Plain, Pos: 0})
		}
		if d == targetDepth {
			_ = cur.Declare(name.Decl{Name: target, Kind: name.Plain, Pos: 0})
		}
	}
	return cur
}

func TestLookupCountBound(t *testing.T) {
	cases := []struct {
		label       string
		targetDepth int
		wantScanned int64
		bound       int64
	}{
		{"target at outermost", 0, 1000, 999 + 1}, // <= depth + constant(1)
		{"target at innermost", 999, 1, 1},        // <= small constant(1)
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			r := &Resolver{}
			inner := buildChain(1000, 50, "target", tc.targetDepth)
			hit, err := r.Resolve(inner, "target", 0)
			if err != nil || hit.Depth != tc.targetDepth {
				t.Fatalf("got hit=%+v err=%v", hit, err)
			}
			if got := r.scanned.Load(); got != tc.wantScanned || got > tc.bound {
				t.Fatalf("scanned=%d want=%d bound=%d", got, tc.wantScanned, tc.bound)
			}
		})
	}
}

func TestResolveSemantics(t *testing.T) {
	mk := func() (*scope.Scope, *scope.Scope) {
		root := scope.New(nil, 0)
		_ = root.Declare(name.Decl{Name: "x", Kind: name.Plain, Pos: 0})
		_ = root.Declare(name.Decl{Name: "p", Kind: name.Plain, Pos: 10})
		_ = root.Declare(name.Decl{Name: "f", Kind: name.Forward, Pos: 10})
		inner := scope.New(root, 1)
		_ = inner.Declare(name.Decl{Name: "x", Kind: name.Forward, Pos: 10})
		return root, inner
	}
	cases := []struct {
		label     string
		from      int // 0 = root, 1 = inner
		name      string
		pos       int
		wantDepth int
		wantPos   int
		wantErr   error
	}{
		{"hit same scope", 0, "x", 0, 0, 0, nil},
		{"hit outer from inner", 1, "p", 10, 0, 10, nil},
		{"inner shadows outer", 1, "x", 10, 1, 10, nil},
		{"forward allows early pos", 0, "f", 3, 0, 10, nil},
		{"inner fwd shadows outer before decl pos", 1, "x", 3, 1, 10, nil},
		{"plain early pos rejected", 0, "p", 3, 0, 0, ErrUseBeforeDecl},
		{"plain early does not fall through", 1, "p", 3, 0, 0, ErrUseBeforeDecl},
		{"undefined", 1, "nope", 0, 0, 0, ErrUndefined},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			root, inner := mk()
			from := []*scope.Scope{root, inner}[tc.from]
			hit, err := (&Resolver{}).Resolve(from, tc.name, tc.pos)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (hit.Depth != tc.wantDepth || hit.Decl.Pos != tc.wantPos) {
				t.Fatalf("hit=%+v want depth=%d pos=%d", hit, tc.wantDepth, tc.wantPos)
			}
		})
	}
	if errors.Is(ErrUndefined, ErrUseBeforeDecl) || errors.Is(ErrUseBeforeDecl, ErrUndefined) {
		t.Fatal("ErrUndefined and ErrUseBeforeDecl must be distinct")
	}
}
