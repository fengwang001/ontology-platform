package predicate

import (
	"errors"
	"testing"
)

func TestValidateAndCount(t *testing.T) {
	valid := []struct {
		name string
		n    *Node
	}{
		{"const", Const(true)},
		{"col eq", Col("a", OpEq)},
		{"col is null", Col("a", OpIsNull)},
		{"not", Not(Col("a", OpEq))},
		{"and", And(Col("a", OpEq), Col("b", OpIsNull))},
		{"or", Or(Const(false), Col("a", OpGt))},
		{"nested", And(Or(Col("a", OpEq), Not(Col("b", OpEq)))), Const(true))},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.n); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
	invalid := []struct {
		name string
		n    *Node
	}{
		{"nil", nil},
		{"unknown kind", &Node{Kind: "xor"}},
		{"bad op", Col("a", "??")},
		{"empty column", Col("", OpEq)},
		{"not no child", Not(nil)},
		{"and one child", And(Col("a", OpEq))},
		{"or no child", Or()},
		{"nested bad", And(Col("a", OpEq), Col("", OpEq))},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(Validate(tt.n), ErrInvalidPredicate) {
				t.Fatalf("Validate = %v, want ErrInvalidPredicate", Validate(tt.n))
			}
		})
	}
	counts := []struct {
		n    *Node
		want int
	}{
		{Const(true), 1},
		{Not(Col("a", OpEq)), 2},
		{And(Col("a", OpEq), Col("b", OpEq)), 3},
		{Or(Const(false), Not(Col("a", OpEq))), 4},
	}
	for i, tt := range counts {
		if got := Count(tt.n); got != tt.want {
			t.Fatalf("case %d Count = %d, want %d", i, got, tt.want)
		}
	}
}
