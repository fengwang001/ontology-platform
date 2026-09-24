package predicate

import (
	"reflect"
	"testing"
)

func TestCount(t *testing.T) {
	cases := []struct {
		name string
		node *Node
		want int
	}{
		{"nil", nil, 0},
		{"leaf", Equal("a", 1), 1},
		{"not", NotOf(Equal("a", 1)), 2},
		{"and-or", AndOf(Equal("a", 1), OrOf(Equal("b", 2), IsNullOf("c"))), 5},
		{"nested not", NotOf(NotOf(ConstBool(true))), 3},
	}
	for _, c := range cases {
		if got := Count(c.node); got != c.want {
			t.Errorf("%s: Count = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestFold(t *testing.T) {
	eq := func() *Node { return Equal("secret", 1) }
	cases := []struct {
		name string
		in   *Node
		want *Node
	}{
		{"nil", nil, nil},
		{"leaf untouched", eq(), eq()},
		{"NOT TRUE", NotOf(ConstBool(true)), ConstBool(false)},
		{"NOT NOT FALSE", NotOf(NotOf(ConstBool(false))), ConstBool(false)},
		{"AND TRUE drops neutral", AndOf(ConstBool(true), eq()), eq()},
		{"AND FALSE short-circuits", AndOf(eq(), ConstBool(false)), ConstBool(false)},
		{"OR TRUE short-circuits", OrOf(eq(), ConstBool(true)), ConstBool(true)},
		{"OR FALSE drops neutral", OrOf(ConstBool(false), eq()), eq()},
		{"empty AND is TRUE", AndOf(), ConstBool(true)},
		{"empty OR is FALSE", OrOf(), ConstBool(false)},
		{"nested fold", AndOf(OrOf(ConstBool(false), eq()), ConstBool(true)), eq()},
		{"no constants", AndOf(eq(), NotOf(eq())), AndOf(eq(), NotOf(eq()))},
	}
	for _, c := range cases {
		if got := Fold(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Fold = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestFoldDoesNotMutate: folding must build a new tree, leaving the input
// intact for the caller.
func TestFoldDoesNotMutate(t *testing.T) {
	in := AndOf(ConstBool(true), Equal("a", 1))
	before := Count(in)
	_ = Fold(in)
	if Count(in) != before || in.Kind != And || len(in.Kids) != 2 {
		t.Error("Fold must not mutate its input")
	}
}
