package predicate

import "testing"

func TestEval(t *testing.T) {
	row := map[string]any{"n": 3, "s": "x", "nilcol": nil}
	cases := []struct {
		name string
		pred *Node
		want bool
	}{
		{"eq true", Cmp("n", Eq, 3), true},
		{"eq false", Cmp("n", Eq, 4), false},
		{"ne", Cmp("n", Ne, 4), true},
		{"lt", Cmp("n", Lt, 4), true},
		{"gt", Cmp("n", Gt, 4), false},
		{"le", Cmp("n", Le, 3), true},
		{"ge", Cmp("n", Ge, 3), true},
		{"string eq", Cmp("s", Eq, "x"), true},
		{"is null", Cmp("nilcol", IsNull, nil), true},
		{"is not null", Cmp("n", IsNotNul, nil), true},
		{"const true", Boolean(true), true},
		{"const false", Boolean(false), false},
		{"and", AndAll(Cmp("n", Eq, 3), Cmp("s", Eq, "x")), true},
		{"and false", AndAll(Cmp("n", Eq, 3), Cmp("s", Eq, "y")), false},
		{"or", OrAll(Cmp("n", Eq, 9), Cmp("s", Eq, "x")), true},
		{"or false", OrAll(Cmp("n", Eq, 9), Boolean(false)), false},
		{"not", NotNode(Cmp("n", Eq, 9)), true},
		{"nested", NotNode(AndAll(Cmp("n", Gt, 1), OrAll(Boolean(false), Cmp("s", Eq, "x")))), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Eval(c.pred, row)
			if err != nil || got != c.want {
				t.Fatalf("got %v, err %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestEvalErrors(t *testing.T) {
	if _, err := Eval(Cmp("ghost", Eq, 1), map[string]any{}); err == nil {
		t.Fatal("missing column must error")
	}
	if _, err := Eval(Cmp("s", Eq, 1), map[string]any{"s": "x"}); err == nil {
		t.Fatal("incomparable types must error")
	}
	if _, err := Eval(Cmp("s", Op("??"), 1), map[string]any{"s": "x"}); err == nil {
		t.Fatal("unknown op must error")
	}
	if _, err := Eval(&Node{Kind: Kind(99)}, map[string]any{}); err == nil {
		t.Fatal("unknown kind must error")
	}
}

func TestNodeCount(t *testing.T) {
	cases := []struct {
		tree *Node
		want int
	}{
		{nil, 0},
		{Boolean(true), 1},
		{Cmp("a", Eq, 1), 1},
		{NotNode(Cmp("a", Eq, 1)), 2},
		{AndAll(Cmp("a", Eq, 1), OrAll(Cmp("b", Eq, 2), Cmp("c", Eq, 3))), 5},
	}
	for i, c := range cases {
		if got := NodeCount(c.tree); got != c.want {
			t.Fatalf("case %d: got %d, want %d", i, got, c.want)
		}
	}
}
