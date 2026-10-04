package doc

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// leafSpec is a path=typed-value expectation rendered as a string.
func flat(root *Node) []string {
	fs := Flatten(root)
	out := make([]string, len(fs))
	for i, f := range fs {
		switch f.Node.Kind {
		case Int:
			out[i] = fmt.Sprintf("%s=i:%d", f.Path, f.Node.I)
		case Str:
			out[i] = fmt.Sprintf("%s=s:%q", f.Path, f.Node.S)
		case Bool:
			out[i] = fmt.Sprintf("%s=s:b:%t", f.Path, f.Node.B)
		}
	}
	return out
}

func b(v ...bool) string {
	if len(v) == 0 || !v[0] {
		return "false"
	}
	return "true"
}

func boolTag(path string, v bool) string { return fmt.Sprintf("%s=s:b:%t", path, v) }

func mergeCase(doc0 *Node, patch *Node, limit int) (*Result, error, *int, []string, []string) {
	p, err := NewPatch(patch)
	if err != nil {
		return nil, err, nil, nil, nil
	}
	RebuildCounts(doc0)
	n := Leaves(doc0)
	var sets, dels []string
	h := Hooks{
		Set: func(path string, old, leaf *Node) {
			tag := "nil"
			if old != nil {
				switch old.Kind {
				case Int:
					tag = fmt.Sprintf("i:%d", old.I)
				case Str:
					tag = fmt.Sprintf("s:%q", old.S)
				case Bool:
					tag = fmt.Sprintf("b:%t", old.B)
				case Object:
					tag = "OBJ"
				}
			}
			sets = append(sets, path+"<-"+tag)
		},
		Delete: func(path string, old *Node) {
			dels = append(dels, path+":"+string(rune('0'+Size(old))))
		},
	}
	r, err := Merge(doc0, p, &n, limit, h)
	sort.Strings(sets)
	sort.Strings(dels)
	if err != nil {
		return &r, err, &n, sets, dels
	}
	return &r, nil, &n, sets, dels
}

func TestMergeTable(t *testing.T) {
	longKey := strings.Repeat("x", 64)
	cases := []struct {
		name    string
		doc0    *Node
		patch   *Node
		limit   int
		wantErr error
		changed bool
		leaves  int
		want    []string
	}{
		{
			name:    "example1 desired",
			doc0:    NewObject(),
			patch:   Obj("a", Obj("b", IntLeaf(1), "c", IntLeaf(2)), "d", StringLeaf("x")),
			changed: true,
			leaves:  3,
			want:    []string{"a.b=i:1", "a.c=i:2", `d=s:"x"`},
		},
		{
			name:    "empty-object patch on leaf deletes it",
			doc0:    Obj("k", IntLeaf(1)),
			patch:   Obj("k", Obj()),
			changed: true,
			leaves:  0,
			want:    nil,
		},
		{
			name:    "empty-object patch on missing key is no-op",
			doc0:    NewObject(),
			patch:   Obj("k", Obj()),
			changed: false,
			leaves:  0,
		},
		{
			name:    "empty-object patch on non-empty object leaves it unchanged",
			doc0:    Obj("k", Obj("x", IntLeaf(1))),
			patch:   Obj("k", Obj()),
			changed: false,
			leaves:  1,
			want:    []string{"k.x=i:1"},
		},
		{
			name:    "null on missing key is no-op",
			doc0:    NewObject(),
			patch:   Obj("k", NullNode()),
			changed: false,
			leaves:  0,
		},
		{
			name:    "null removes leaf and prunes",
			doc0:    Obj("a", Obj("b", IntLeaf(1), "c", IntLeaf(2))),
			patch:   Obj("a", Obj("c", NullNode())),
			changed: true,
			leaves:  1,
			want:    []string{"a.b=i:1"},
		},
		{
			name:    "leaf replaces whole object subtree",
			doc0:    Obj("a", Obj("b", Obj("c", IntLeaf(9)))),
			patch:   Obj("a", IntLeaf(1)),
			changed: true,
			leaves:  1,
			want:    []string{"a=i:1"},
		},
		{
			name:    "object replaces leaf",
			doc0:    Obj("m", IntLeaf(7)),
			patch:   Obj("m", Obj("x", IntLeaf(1))),
			changed: true,
			leaves:  1,
			want:    []string{"m.x=i:1"},
		},
		{
			name:    "same typed leaf unchanged",
			doc0:    Obj("d", StringLeaf("x")),
			patch:   Obj("d", StringLeaf("x")),
			changed: false,
			leaves:  1,
			want:    []string{`d=s:"x"`},
		},
		{
			name:    "int vs string differ",
			doc0:    Obj("k", IntLeaf(1)),
			patch:   Obj("k", StringLeaf("1")),
			changed: true,
			leaves:  1,
			want:    []string{`k=s:"1"`},
		},
		{
			name:    "example4 a.b-emptied prunes a",
			doc0:    Obj("a", Obj("b", IntLeaf(1)), "d", StringLeaf("x")),
			patch:   Obj("a", Obj("b", Obj())),
			changed: true,
			leaves:  1,
			want:    []string{`d=s:"x"`},
		},
		{
			name:    "depth exactly four segments",
			doc0:    NewObject(),
			patch:   Obj("a", Obj("b", Obj("c", Obj("d", IntLeaf(1))))),
			changed: true,
			leaves:  1,
			want:    []string{"a.b.c.d=i:1"},
		},
		{
			name:    "depth five segments invalid",
			doc0:    NewObject(),
			patch:   Obj("a", Obj("b", Obj("c", Obj("d", Obj("e", IntLeaf(1)))))),
			wantErr: ErrInvalid,
		},
		{
			name:    "deep null still invalid by patch shape",
			doc0:    NewObject(),
			patch:   Obj("a", Obj("b", Obj("c", Obj("d", Obj("e", NullNode()))))),
			wantErr: ErrInvalid,
		},
		{
			name:    "64-byte key accepted",
			doc0:    NewObject(),
			patch:   Obj(longKey, BoolLeaf(true)),
			changed: true,
			leaves:  1,
			want:    []string{boolTag(longKey, true)},
		},
		{
			name:    "65-byte key invalid",
			doc0:    NewObject(),
			patch:   Obj(strings.Repeat("y", 65), IntLeaf(1)),
			wantErr: ErrInvalid,
		},
		{
			name:    "dot in key invalid",
			doc0:    NewObject(),
			patch:   Obj("a.b", IntLeaf(1)),
			wantErr: ErrInvalid,
		},
		{
			name:    "empty root patch invalid",
			doc0:    NewObject(),
			patch:   Obj(),
			wantErr: ErrInvalid,
		},
		{
			name:    "leaf count exactly at limit",
			doc0:    Obj("a", IntLeaf(1)),
			patch:   Obj("b", IntLeaf(2)),
			limit:   2,
			changed: true,
			leaves:  2,
			want:    []string{"a=i:1", "b=i:2"},
		},
		{
			name:    "leaf count one over limit",
			doc0:    Obj("a", IntLeaf(1)),
			patch:   Obj("b", IntLeaf(2)),
			limit:   1,
			wantErr: ErrTooLarge,
			leaves:  1,
			want:    []string{"a=i:1"},
		},
		{
			name:    "shrink-and-grow net within limit",
			doc0:    Obj("a", Obj("u", IntLeaf(1), "v", IntLeaf(2))),
			patch:   Obj("a", NullNode(), "z", IntLeaf(3)),
			limit:   1,
			changed: true,
			leaves:  1,
			want:    []string{"z=i:3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err, n, _, _ := mergeCase(tc.doc0, tc.patch, tc.limit)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if got := flat(tc.doc0); len(got) != tc.leaves {
					t.Fatalf("state changed on rejection: leaves=%d want %d (%v)", n, tc.leaves, got)
				}
				return
			}
			if r.Changed != tc.changed {
				t.Fatalf("changed=%v want %v", r.Changed, tc.changed)
			}
			if *n != tc.leaves || r.Leaves != tc.leaves {
				t.Fatalf("leaves=%d (res %d), want %d", *n, r.Leaves, tc.leaves)
			}
			if got := flat(tc.doc0); !eqStrings(got, tc.want) {
				t.Fatalf("doc = %v, want %v", got, tc.want)
			}
		})
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMergeHooksAndTouched(t *testing.T) {
	doc0 := Obj("a", Obj("b", IntLeaf(1), "c", StringLeaf("q")), "d", BoolLeaf(true))
	patch := Obj("a", Obj("b", IntLeaf(9), "c", NullNode()), "d", BoolLeaf(true), "e", Obj())
	r, err, _, sets, dels := mergeCase(doc0, patch, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed {
		t.Fatal("want changed")
	}
	if got := flat(doc0); !eqStrings(got, []string{"a.b=i:9", boolTag("d", true)}) {
		t.Fatalf("doc=%v", got)
	}
	// touched: a (object), a.b (unchanged leaf), a.c (deleted leaf), d (unchanged leaf) = 4
	if r.Touched != 4 {
		t.Fatalf("touched=%d want 4", r.Touched)
	}
	if !eqStrings(sets, []string{"a.b<-i:1"}) {
		t.Fatalf("sets=%v", sets)
	}
	if !eqStrings(dels, []string{"a.c:1"}) {
		t.Fatalf("dels=%v", dels)
	}
}

func TestTouchedIndependentOfUnrelatedSize(t *testing.T) {
	patch := Obj("z", Obj("p", Obj("q", Obj("r", IntLeaf(7)))), "zz", Obj())
	build := func(n int) *Node {
		root := NewObject()
		for i := 0; i < n; i++ {
			root.Kids[fmt.Sprintf("f%05d", i)] = Obj("x", IntLeaf(int64(i)))
		}
		return root
	}
	var touched []int
	for _, n := range []int{100, 10000} {
		d := build(n)
		p, _ := NewPatch(patch)
		cnt := Leaves(d)
		r, err := Merge(d, p, &cnt, 0, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		touched = append(touched, r.Touched)
	}
	if touched[0] != touched[1] || touched[0] == 0 {
		t.Fatalf("touched not independent: %v", touched)
	}
}
