package predicate

import "testing"

func TestBuildersAndCount(t *testing.T) {
	tree := And(Cmp("a", Eq, 1), Or(Not(Cmp("b", IsNull, nil)), Const(true)))
	cases := []struct {
		name string
		node *Node
		kind Kind
		want int
	}{
		{"nil", nil, 0, 0},
		{"const", Const(false), KindConst, 1},
		{"cmp", Cmp("a", Ne, 2), KindCmp, 1},
		{"not", Not(Cmp("a", Eq, 1)), KindNot, 2},
		{"tree", tree, KindAnd, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.node != nil && tc.node.Kind != tc.kind {
				t.Fatalf("kind = %d, want %d", tc.node.Kind, tc.kind)
			}
			if got := Count(tc.node); got != tc.want {
				t.Fatalf("count = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCmpLeafFields(t *testing.T) {
	cases := []struct {
		name       string
		leaf       *Node
		column     string
		op         CmpOp
		value      any
		childCount int
	}{
		{"eq", Cmp("secret", Eq, 1), "secret", Eq, 1, 0},
		{"isnull", Cmp("secret", IsNull, nil), "secret", IsNull, nil, 0},
		{"isnotnull", Cmp("secret", IsNotNull, nil), "secret", IsNotNull, nil, 0},
		{"lt", Cmp("x", Lt, 3), "x", Lt, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.leaf.Column != tc.column || tc.leaf.Op != tc.op || tc.leaf.Value != tc.value {
				t.Fatalf("leaf fields mismatch: %+v", tc.leaf)
			}
			if len(tc.leaf.Children) != tc.childCount {
				t.Fatalf("children = %d, want 0", len(tc.leaf.Children))
			}
		})
	}
}

func TestPath(t *testing.T) {
	cases := []struct {
		path  Path
		other Path
		str   string
		equal bool
	}{
		{Path{}, Path{}, "[]", true},
		{Path{1}, Path{1}, "[1]", true},
		{Path{1, 0}, Path{1, 0}, "[1,0]", true},
		{Path{1, 0}, Path{0, 1}, "[1,0]", false},
		{Path{1, 0}, Path{1}, "[1,0]", false},
	}
	for _, tc := range cases {
		t.Run(tc.str, func(t *testing.T) {
			if got := tc.path.String(); got != tc.str {
				t.Fatalf("String() = %q, want %q", got, tc.str)
			}
			if got := tc.path.Equal(tc.other); got != tc.equal {
				t.Fatalf("Equal() = %v, want %v", got, tc.equal)
			}
		})
	}
}
