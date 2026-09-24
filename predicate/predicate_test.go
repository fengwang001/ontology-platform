package predicate

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		node *Node
		want bool
	}{
		{"nil", nil, true},
		{"eq", NewEq("a", "1"), true},
		{"isnull", NewIsNull("a"), true},
		{"not", NewNot(NewEq("a", "1")), true},
		{"and2", NewAnd(NewEq("a", "1"), NewEq("b", "2")), true},
		{"or3", NewOr(NewEq("a", "1"), NewEq("b", "2"), NewEq("c", "3")), true},
		{"and one kid", NewAnd(NewEq("a", "1")), false},
		{"not two kids", &Node{Op: Not, Kids: []*Node{NewEq("a", "1"), NewEq("b", "2")}}, false},
		{"eq no column", NewEq("", "1"), false},
		{"unknown op", &Node{Op: Op(99)}, false},
	}
	for _, tc := range cases {
		err := Validate(tc.node)
		if (err == nil) != tc.want || err != nil && !errors.Is(err, ErrInvalidPredicate) {
			t.Errorf("%s: err=%v want invalid=%v", tc.name, err, tc.want)
		}
	}
}

func TestCount(t *testing.T) {
	cases := []struct {
		node *Node
		want int
	}{
		{nil, 0},
		{NewEq("a", "1"), 1},
		{NewNot(NewEq("a", "1")), 2},
		{NewAnd(NewEq("a", "1"), NewEq("b", "2"), NewEq("c", "3")), 4},
		{NewOr(NewAnd(NewConst(true), NewEq("a", "1")), NewEq("b", "2")), 5},
	}
	for i, tc := range cases {
		if got := Count(tc.node); got != tc.want {
			t.Errorf("case %d: Count=%d want %d", i, got, tc.want)
		}
	}
}

func TestFold(t *testing.T) {
	cases := []struct {
		name    string
		node    *Node
		isConst bool
		val     bool
	}{
		{"nil is vacuously constant", nil, true, false},
		{"const", NewConst(true), true, true},
		{"column not constant", NewEq("a", "1"), false, false},
		{"and false absorbs", NewAnd(NewConst(false), NewEq("secret", "1")), true, false},
		{"or true absorbs", NewOr(NewConst(true), NewEq("secret", "1")), true, true},
		{"and true removed", NewAnd(NewConst(true), NewEq("a", "1")), false, false},
		{"or false removed", NewOr(NewConst(false), NewEq("a", "1")), false, false},
		{"not constant", NewNot(NewConst(true)), true, false},
		{"not column", NewNot(NewEq("a", "1")), false, false},
		{"all constants and", NewAnd(NewConst(true), NewConst(false)), true, false},
		{"all constants or", NewOr(NewConst(false), NewConst(true)), true, true},
	}
	for _, tc := range cases {
		got, isConst, val := Fold(tc.node)
		if isConst != tc.isConst || val != tc.val {
			t.Errorf("%s: const=%v val=%v want %v/%v", tc.name, isConst, val, tc.isConst, tc.val)
		}
		if err := Validate(got); err != nil {
			t.Errorf("%s: folded tree invalid: %v", tc.name, err)
		}
	}
}
