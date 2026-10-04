package doc

import (
	"reflect"
	"testing"
)

func mkpatch(t *testing.T, raw map[string]any) map[string]any {
	t.Helper()
	p, err := ParsePatch(raw)
	if err != nil {
		t.Fatalf("ParsePatch(%v): %v", raw, err)
	}
	return p
}

func mergeOnce(t *testing.T, root *Node[int], raw map[string]any, ver int) (*Node[int], *Report[int]) {
	t.Helper()
	p := mkpatch(t, raw)
	r := Merge(root, p, func() int { return ver })
	return r.Root, r
}

func leafMap(root *Node[int]) map[string]any {
	out := map[string]any{}
	for _, lp := range Leaves(root) {
		out[lp.Path] = lp.Leaf.Value
	}
	return out
}

func mvMap(root *Node[int]) map[string]int {
	out := map[string]int{}
	for _, lp := range Leaves(root) {
		out[lp.Path] = lp.Leaf.Meta
	}
	return out
}

func TestValidKey(t *testing.T) {
	bad := []string{"", "a.b", string(make([]byte, 65))}
	for _, k := range bad {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) = true, want false", k)
		}
	}
	if !ValidKey("a") || !ValidKey("汉字-key") || !ValidKey(string(make([]byte, 64))) {
		t.Errorf("ValidKey rejected a valid key")
	}
}

func TestParsePatchErrors(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
	}{
		{"nil", nil},
		{"empty", map[string]any{}},
		{"bad key dot", map[string]any{"a.b": 1}},
		{"bad key empty", map[string]any{"": 1}},
		{"bad value", map[string]any{"a": float64(1)}},
		{"5 segments", nest([]string{"a", "b", "c", "d", "e"}, int64(1))},
		{"null at 5", nest([]string{"a", "b", "c", "d", "e"}, nil)},
		{"non-empty object at depth 4 invalid", nest([]string{"a", "b", "c", "d"}, map[string]any{"e": int64(1)})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParsePatch(tc.in); err == nil {
				t.Fatalf("ParsePatch(%v) succeeded, want error", tc.in)
			}
		})
	}
	// 4 segments is valid.
	if _, err := ParsePatch(nest([]string{"a", "b", "c", "d"}, int64(1))); err != nil {
		t.Fatalf("4-segment path rejected: %v", err)
	}
	// int normalizes to int64.
	p, err := ParsePatch(map[string]any{"a": 1})
	if err != nil || reflect.TypeOf(p["a"]) != reflect.TypeOf(int64(0)) {
		t.Fatalf("int normalization failed: %#v err=%v", p, err)
	}
}

func nest(keys []string, v any) map[string]any {
	cur := map[string]any{keys[len(keys)-1]: v}
	for i := len(keys) - 2; i >= 0; i-- {
		cur = map[string]any{keys[i]: cur}
	}
	return cur
}

func TestMergeTable(t *testing.T) {
	cases := []struct {
		name   string
		setup  map[string]any
		patch  map[string]any
		want   map[string]any
		events []Event[int]
	}{
		{
			name:  "simple add",
			patch: map[string]any{"a": int64(1), "b": "x", "c": true},
			want:  map[string]any{"a": int64(1), "b": "x", "c": true},
			events: []Event[int]{
				{Path: "a", Type: Added}, {Path: "b", Type: Added}, {Path: "c", Type: Added},
			},
		},
		{
			name:  "nested add",
			patch: map[string]any{"a": map[string]any{"b": int64(1), "c": int64(2)}, "d": "x"},
			want:  map[string]any{"a.b": int64(1), "a.c": int64(2), "d": "x"},
			events: []Event[int]{
				{Path: "a.b", Type: Added}, {Path: "a.c", Type: Added}, {Path: "d", Type: Added},
			},
		},
		{
			name:  "empty object on leaf deletes it",
			setup: map[string]any{"a": int64(5), "keep": "y"},
			patch: map[string]any{"a": map[string]any{}},
			want:  map[string]any{"keep": "y"},
			events: []Event[int]{
				{Path: "a", Type: Removed},
			},
		},
		{
			name:   "empty object on absent key is no-op",
			patch:  map[string]any{"e": map[string]any{}},
			want:   map[string]any{},
			events: nil,
		},
		{
			name:   "empty object on non-empty object is no-op",
			setup:  map[string]any{"a": map[string]any{"x": int64(1)}},
			patch:  map[string]any{"a": map[string]any{}},
			want:   map[string]any{"a.x": int64(1)},
			events: nil,
		},
		{
			name:   "null on absent key is no-op",
			patch:  map[string]any{"z": nil},
			want:   map[string]any{},
			events: nil,
		},
		{
			name:  "null removes leaf",
			setup: map[string]any{"a": int64(1), "b": int64(2)},
			patch: map[string]any{"a": nil},
			want:  map[string]any{"b": int64(2)},
			events: []Event[int]{
				{Path: "a", Type: Removed},
			},
		},
		{
			name:  "null removes one leaf but keeps sibling",
			setup: map[string]any{"a": map[string]any{"b": map[string]any{"x": int64(1), "y": int64(2)}}, "z": true},
			patch: map[string]any{"a": map[string]any{"b": map[string]any{"x": nil}}},
			want:  map[string]any{"a.b.y": int64(2), "z": true},
			events: []Event[int]{
				{Path: "a.b.x", Type: Removed},
			},
		},
		{
			name:  "null removes last leaf and empties propagate upward",
			setup: map[string]any{"a": map[string]any{"b": map[string]any{"x": int64(1)}}, "z": true},
			patch: map[string]any{"a": map[string]any{"b": map[string]any{"x": nil}}},
			want:  map[string]any{"z": true},
			events: []Event[int]{
				{Path: "a.b.x", Type: Removed},
			},
		},
		{
			name:   "identical leaf value is no-op",
			setup:  map[string]any{"a": int64(1)},
			patch:  map[string]any{"a": int64(1)},
			want:   map[string]any{"a": int64(1)},
			events: nil,
		},
		{
			name:  "int vs string are different",
			setup: map[string]any{"a": int64(1)},
			patch: map[string]any{"a": "1"},
			want:  map[string]any{"a": "1"},
			events: []Event[int]{
				{Path: "a", Type: Changed},
			},
		},
		{
			name:  "object replaces leaf subtree",
			setup: map[string]any{"a": int64(9)},
			patch: map[string]any{"a": map[string]any{"b": int64(1)}},
			want:  map[string]any{"a.b": int64(1)},
			events: []Event[int]{
				{Path: "a", Type: Removed}, {Path: "a.b", Type: Added},
			},
		},
		{
			name:  "leaf replaces object subtree",
			setup: map[string]any{"a": map[string]any{"b": int64(1), "c": int64(2)}},
			patch: map[string]any{"a": int64(7)},
			want:  map[string]any{"a": int64(7)},
			events: []Event[int]{
				{Path: "a", Type: Added}, {Path: "a.b", Type: Removed}, {Path: "a.c", Type: Removed},
			},
		},
		{
			name:  "merge into object keeps untouched siblings",
			setup: map[string]any{"a": map[string]any{"x": int64(1), "y": int64(2)}},
			patch: map[string]any{"a": map[string]any{"y": int64(3), "z": true}},
			want:  map[string]any{"a.x": int64(1), "a.y": int64(3), "a.z": true},
			events: []Event[int]{
				{Path: "a.y", Type: Changed}, {Path: "a.z", Type: Added},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRoot[int]()
			if tc.setup != nil {
				p := mkpatch(t, tc.setup)
				root = Merge(root, p, func() int { return 0 }).Root
			}
			root, r := mergeOnce(t, root, tc.patch, 1)
			got := leafMap(root)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("leaves = %v, want %v", got, tc.want)
			}
			var gotEvents []Event[int]
			for _, e := range r.Events {
				gotEvents = append(gotEvents, Event[int]{Path: e.Path, Type: e.Type})
			}
			if !reflect.DeepEqual(gotEvents, tc.events) {
				t.Errorf("events = %v, want %v", gotEvents, tc.events)
			}
			if r.LeafCount != len(tc.want) {
				t.Errorf("LeafCount = %d, want %d", r.LeafCount, len(tc.want))
			}
		})
	}
}

func TestMergeMetadata(t *testing.T) {
	root := NewRoot[int]()
	root = Merge(root, mkpatch(t, map[string]any{"a": map[string]any{"b": int64(1)}, "d": "x"}),
		func() int { return 1 }).Root
	root = Merge(root, mkpatch(t, map[string]any{"a": map[string]any{"b": int64(1)}, "d": "x"}),
		func() int { return 2 }).Root
	mv := mvMap(root)
	if mv["a.b"] != 1 || mv["d"] != 1 {
		t.Fatalf("unchanged leaves must keep mv: %v", mv)
	}
	// subtree replacement gives new leaves the current version.
	root = Merge(root, mkpatch(t, map[string]any{"a": int64(9)}),
		func() int { return 3 }).Root
	mv = mvMap(root)
	if mv["a"] != 3 {
		t.Fatalf("replaced subtree leaf mv = %d, want 3: %v", mv["a"], mv)
	}
	// type change creates a new leaf with new mv.
	root = Merge(root, mkpatch(t, map[string]any{"a": "9"}),
		func() int { return 4 }).Root
	if got := mvMap(root)["a"]; got != 4 {
		t.Fatalf("changed leaf mv = %d, want 4", got)
	}
}

func TestTouchedOnlyDependsOnPatchAndReplacedSubtrees(t *testing.T) {
	patch := map[string]any{
		"p1": map[string]any{"q": int64(1)},
		"p2": int64(2),
		"p3": "s",
	}
	build := func(n int) *Node[int] {
		m := map[string]any{}
		for i := 0; i < n; i++ {
			m[padKey(i)] = int64(i)
		}
		return Merge(NewRoot[int](), mkpatch(t, m), func() int { return 0 }).Root
	}
	_, small := mergeOnce(t, build(100), patch, 1)
	_, large := mergeOnce(t, build(10000), patch, 1)
	if small.touched != large.touched {
		t.Fatalf("touched differs across document sizes: %d vs %d", small.touched, large.touched)
	}
	// null of an existing subtree touches its nodes too; the two docs must
	// still match since the deleted subtree has identical shape.
	withSub := func(n int) *Node[int] {
		r := build(n)
		r = Merge(r, mkpatch(t, map[string]any{"p1": map[string]any{"q": int64(0)}}),
			func() int { return 0 }).Root
		return r
	}
	_, s2 := mergeOnce(t, withSub(100), map[string]any{"p1": nil}, 2)
	_, l2 := mergeOnce(t, withSub(10000), map[string]any{"p1": nil}, 2)
	if s2.touched != l2.touched {
		t.Fatalf("touched differs for subtree delete: %d vs %d", s2.touched, l2.touched)
	}
}

func padKey(i int) string {
	s := "key"
	for n := i; n >= 0; n = n/26 - 1 {
		s += string(rune('a' + (n % 26)))
		if n < 26 {
			break
		}
	}
	return s
}
