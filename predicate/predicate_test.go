package predicate

import "testing"

func TestCount(t *testing.T) {
	tests := []struct {
	name string
	n    Node
	want int
	}{
		{"nil", nil, 0},
		{"const", Const{Value: true}, 1},
		{"cmp", Cmp{Col: "a", Op: OpEq, Value: 1}, 1},
		{"not", Not{X: Cmp{Col: "a"}}, 2},
		{"and", And{Xs: []Node{Cmp{Col: "a"}, Cmp{Col: "b"}, Const{}}}, 4},
		{"or nested", Or{Xs: []Node{Cmp{Col: "a"}, And{Xs: []Node{Cmp{Col: "b"}, Const{}}}}}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Count(tt.n); got != tt.want {
				t.Fatalf("Count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFold(t *testing.T) {
	tests := []struct {
	name  string
	n     Node
	isConst bool
	want  bool
	}{
		{"nil folds true", nil, true, true},
		{"not const", Not{X: Const{Value: true}}, true, false},
		{"or false arm absorbed", Or{Xs: []Node{Const{Value: false}, Cmp{Col: "a"}}}, false, false},
		{"or true arm dominates", Or{Xs: []Node{Const{Value: true}, Cmp{Col: "a"}}}, true, true},
		{"and true arm absorbed", And{Xs: []Node{Const{Value: true}, Cmp{Col: "a"}}}, false, false},
		{"and false dominates", And{Xs: []Node{Const{Value: false}, Cmp{Col: "a"}}}, true, false},
		{"all const or", Or{Xs: []Node{Const{Value: false}, Const{Value: false}}}, true, false},
		{"hidden cmp under not const does not fold", Not{X: Cmp{Col: "secret"}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isConst := Fold(tt.n)
			if isConst != tt.isConst {
				t.Fatalf("const = %v, want %v", isConst, tt.isConst)
			}
			if isConst && got.(Const).Value != tt.want {
				t.Fatalf("value = %v, want %v", got.(Const).Value, tt.want)
			}
		})
	}
}

func TestEval(t *testing.T) {
	row := map[string]any{"a": int64(1), "b": "x", "c": nil}
	tests := []struct {
		name string
		n    Node
		want bool
	}{
		{"nil passes", nil, true},
		{"eq hit", Cmp{Col: "a", Op: OpEq, Value: 1}, true},
		{"neq on nil value", Cmp{Col: "c", Op: OpNeq, Value: 1}, true},
		{"eq on nil value false", Cmp{Col: "c", Op: OpEq, Value: 1}, false},
		{"is null present nil", Cmp{Col: "c", Op: OpIsNull}, true},
		{"is null missing col", Cmp{Col: "ghost", Op: OpIsNull}, true},
		{"is null nonnull", Cmp{Col: "a", Op: OpIsNull}, false},
		{"string lt", Cmp{Col: "b", Op: OpLt, Value: "y"}, true},
		{"not", Not{X: Cmp{Col: "a", Op: OpEq, Value: 2}}, true},
		{"and short", And{Xs: []Node{Cmp{Col: "a", Op: OpEq, Value: 1}, Cmp{Col: "b", Op: OpEq, Value: "z"}}}, false},
		{"or hit", Or{Xs: []Node{Cmp{Col: "b", Op: OpEq, Value: "z"}, Cmp{Col: "a", Op: OpEq, Value: 1}}}, true},
		{"folded true const arm", Or{Xs: []Node{Const{Value: true}, Cmp{Col: "ghost", Op: OpEq, Value: 1}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Eval(tt.n, row); got != tt.want {
				t.Fatalf("Eval = %v, want %v", got, tt.want)
			}
		})
	}
}
