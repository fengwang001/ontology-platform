package predicate

import "testing"

func TestBuildAndCount(t *testing.T) {
	tests := []struct {
		name string
		root *Node
		want int
	}{
		{"nil predicate", nil, 0},
		{"const only", ConstNode(true), 1},
		{"eq compare", Eq("a", "1"), 1},
		{"is null", IsNull("secret"), 1},
		{"not secret=1", Not(Eq("secret", "1")), 2},
		{"and two", And(Eq("a", "1"), IsNull("b")), 3},
		{"or with not", Or(Eq("a", "1"), Not(IsNull("b"))), 4},
		{"nested mixed", And(Or(ConstNode(true), Eq("a", "1")), Not(IsNull("b"))), 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Count(tt.root); got != tt.want {
				t.Fatalf("Count = %d, want %d", got, tt.want)
			}
		})
	}

	if Not(Eq("secret", "1")).Children[0].Op != OpEq {
		t.Fatal("Not must wrap single child")
	}
	if !ConstNode(true).Const || ConstNode(false).Const {
		t.Fatal("ConstNode value wiring broken")
	}
	if IsNull("secret").Op != OpIsNull {
		t.Fatal("IsNull wiring broken")
	}
}
